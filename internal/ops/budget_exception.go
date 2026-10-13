package ops

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/referencecontract"
)

const (
	// MaxBudgetRulingBytes bounds the recorded ruling reference.
	MaxBudgetRulingBytes = 256

	historyExtraBudgetException = "budget_exception"
)

// BudgetException is a human-ruled canonical admission budget raise recorded
// with unblock-task (ADR-0207). It changes only the wall-clock batch budget;
// commands, order, manifest, proofs and deferrals stay as reviewed. Ruling
// names the human decision; the engine records it and does not look it up.
type BudgetException struct {
	TimeoutSeconds int    `json:"timeout_seconds"`
	Ruling         string `json:"ruling"`
}

// Validate checks 1..AcceptanceMaxTimeoutSeconds and a nonblank, trimmed,
// single-line ruling of at most MaxBudgetRulingBytes.
func (e BudgetException) Validate() error {
	if e.TimeoutSeconds < 1 || e.TimeoutSeconds > referencecontract.AcceptanceMaxTimeoutSeconds {
		return fmt.Errorf("budget exception timeout_seconds must be 1..%d", referencecontract.AcceptanceMaxTimeoutSeconds)
	}
	if e.Ruling == "" || strings.TrimSpace(e.Ruling) != e.Ruling || strings.ContainsAny(e.Ruling, "\n\r") ||
		len(e.Ruling) > MaxBudgetRulingBytes || !utf8.ValidString(e.Ruling) {
		return fmt.Errorf("budget exception requires a single-line ruling of at most %d bytes naming the human decision", MaxBudgetRulingBytes)
	}
	return nil
}

func (e BudgetException) historyValue() map[string]any {
	return map[string]any{"timeout_seconds": e.TimeoutSeconds, "ruling": e.Ruling}
}

// RecordedBudgetExceptions returns every valid budget exception recorded on
// the task's unblocked history entries, oldest first.
func RecordedBudgetExceptions(history []models.TaskHistoryEntry) []BudgetException {
	var recorded []BudgetException
	for _, entry := range history {
		if entry.Event != models.TaskEventUnblocked {
			continue
		}
		raw, ok := entry.Extra[historyExtraBudgetException].(map[string]any)
		if !ok {
			continue
		}
		exception := BudgetException{}
		exception.TimeoutSeconds, _ = raw["timeout_seconds"].(int)
		exception.Ruling, _ = raw["ruling"].(string)
		if exception.Validate() == nil {
			recorded = append(recorded, exception)
		}
	}
	return recorded
}

// admissionTimeoutSeconds is the canonical batch budget: the reviewed
// timeout_seconds, raised by the largest recorded budget exception and never
// lowered by one.
func admissionTimeoutSeconds(reviewed int, history []models.TaskHistoryEntry) int {
	budget := reviewed
	for _, exception := range RecordedBudgetExceptions(history) {
		budget = max(budget, exception.TimeoutSeconds)
	}
	return budget
}
