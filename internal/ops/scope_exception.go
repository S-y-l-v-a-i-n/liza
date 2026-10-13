package ops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/statehygiene"
)

const (
	// MaxScopeExceptionBytes bounds one encoded scope exception.
	MaxScopeExceptionBytes = statehygiene.MaxStateTextBytes
	// MaxScopeExceptionFiles bounds the files one exception may add.
	MaxScopeExceptionFiles = 8

	historyExtraScopeException = "scope_exception"
)

// ScopeException is a bounded in-task grant recorded with unblock-task
// (ADR-0206). Outside the task's original scope it permits only Files and
// only Change, and Unchanged must stay as stated. It never alters canonical
// acceptance, provider dependencies, review boundary or lifecycle gates.
type ScopeException struct {
	Files     []string `json:"files"`
	Change    string   `json:"change"`
	Unchanged string   `json:"unchanged"`
}

// RecordedScopeException is a scope exception with its unblock provenance.
type RecordedScopeException struct {
	ScopeException
	Time  time.Time
	Agent string
}

// ParseScopeException decodes exactly one strict JSON object and validates it.
func ParseScopeException(data []byte) (*ScopeException, error) {
	if len(data) > MaxScopeExceptionBytes || !utf8.Valid(data) {
		return nil, fmt.Errorf("scope exception must be UTF-8 and at most %d bytes", MaxScopeExceptionBytes)
	}
	var exception ScopeException
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&exception); err != nil {
		return nil, fmt.Errorf("decoding scope exception: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("scope exception must contain exactly one JSON object")
	}
	if err := exception.Validate(); err != nil {
		return nil, err
	}
	return &exception, nil
}

// Validate checks the bounds: 1..MaxScopeExceptionFiles distinct clean
// repository-relative files, a nonblank change and unchanged statement, and
// an encoded size within MaxScopeExceptionBytes.
func (e ScopeException) Validate() error {
	if len(e.Files) == 0 || len(e.Files) > MaxScopeExceptionFiles {
		return fmt.Errorf("scope exception requires 1 to %d files", MaxScopeExceptionFiles)
	}
	seen := make(map[string]bool, len(e.Files))
	for _, file := range e.Files {
		if strings.TrimSpace(file) != file || file == "" || path.IsAbs(file) || path.Clean(file) != file ||
			file == "." || file == ".." || strings.HasPrefix(file, "../") || strings.ContainsAny(file, "\\\n\r") {
			return fmt.Errorf("scope exception file %q must be a clean repository-relative path", file)
		}
		if seen[file] {
			return fmt.Errorf("scope exception file %q is listed twice", file)
		}
		seen[file] = true
	}
	if strings.TrimSpace(e.Change) == "" {
		return fmt.Errorf("scope exception requires the change it permits")
	}
	if strings.TrimSpace(e.Unchanged) == "" {
		return fmt.Errorf("scope exception requires what must stay unchanged")
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(encoded) > MaxScopeExceptionBytes {
		return fmt.Errorf("scope exception must encode to at most %d bytes", MaxScopeExceptionBytes)
	}
	return nil
}

func (e ScopeException) historyValue() map[string]any {
	return map[string]any{
		"files":     append([]string(nil), e.Files...),
		"change":    e.Change,
		"unchanged": e.Unchanged,
	}
}

// RecordedScopeExceptions returns every scope exception recorded on the
// task's unblocked history entries, oldest first. Grants accumulate for the
// task's lifetime; a later unblock does not withdraw an earlier one.
func RecordedScopeExceptions(history []models.TaskHistoryEntry) []RecordedScopeException {
	var recorded []RecordedScopeException
	for _, entry := range history {
		if entry.Event != models.TaskEventUnblocked {
			continue
		}
		raw, ok := entry.Extra[historyExtraScopeException].(map[string]any)
		if !ok {
			continue
		}
		exception := ScopeException{}
		exception.Change, _ = raw["change"].(string)
		exception.Unchanged, _ = raw["unchanged"].(string)
		// []string before persistence, []any after a YAML round trip.
		switch files := raw["files"].(type) {
		case []string:
			exception.Files = append(exception.Files, files...)
		case []any:
			for _, file := range files {
				if text, ok := file.(string); ok {
					exception.Files = append(exception.Files, text)
				}
			}
		}
		if exception.Validate() != nil {
			continue
		}
		recorded = append(recorded, RecordedScopeException{ScopeException: exception, Time: entry.Time, Agent: derefNonEmpty(entry.Agent)})
	}
	return recorded
}
