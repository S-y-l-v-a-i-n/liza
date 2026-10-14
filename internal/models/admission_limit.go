package models

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Admission-limit bounds (ADR-0209).
const (
	// MaxAdmissionLimitConcurrent bounds the slots one class may declare.
	MaxAdmissionLimitConcurrent = 64
	// MaxAdmissionLimitMatches bounds the substrings one class may declare.
	MaxAdmissionLimitMatches = 16
	// MaxAdmissionLimitMatchBytes bounds one substring.
	MaxAdmissionLimitMatchBytes = 256
)

// admissionLimitClass is also a directory name under the runtime directory.
var admissionLimitClass = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// AdmissionLimit caps how many canonical commands of one resource class
// execute at once across canonical admission and the deferred-validation
// runner. A command belongs to the class when its text contains any Match
// substring (ADR-0209).
type AdmissionLimit struct {
	Class         string   `yaml:"class" json:"class"`
	MaxConcurrent int      `yaml:"max_concurrent" json:"max_concurrent"`
	Match         []string `yaml:"match" json:"match"`
}

// ValidateAdmissionLimit checks the shape the slot directory and matching
// rely on.
func ValidateAdmissionLimit(limit AdmissionLimit) error {
	if !admissionLimitClass.MatchString(limit.Class) {
		return fmt.Errorf("admission limit class %q must match %s", limit.Class, admissionLimitClass)
	}
	if limit.MaxConcurrent < 1 || limit.MaxConcurrent > MaxAdmissionLimitConcurrent {
		return fmt.Errorf("admission limit %s: max_concurrent must be between 1 and %d", limit.Class, MaxAdmissionLimitConcurrent)
	}
	if len(limit.Match) == 0 || len(limit.Match) > MaxAdmissionLimitMatches {
		return fmt.Errorf("admission limit %s: needs 1 to %d match substrings", limit.Class, MaxAdmissionLimitMatches)
	}
	for _, match := range limit.Match {
		if match == "" || len(match) > MaxAdmissionLimitMatchBytes || !utf8.ValidString(match) || strings.ContainsAny(match, "\r\n\x00") {
			return fmt.Errorf("admission limit %s: each match must be a non-empty single-line UTF-8 substring of at most %d bytes", limit.Class, MaxAdmissionLimitMatchBytes)
		}
	}
	return nil
}

// AdmissionLimitsFor returns the valid configured classes that any of
// commands belongs to, sorted by class: the order slots are acquired in.
// Invalid stored entries are skipped, never used as paths.
func AdmissionLimitsFor(config Config, commands []string) []AdmissionLimit {
	var matched []AdmissionLimit
	for _, limit := range config.AdmissionLimits {
		if ValidateAdmissionLimit(limit) != nil || slices.ContainsFunc(matched, func(m AdmissionLimit) bool { return m.Class == limit.Class }) {
			continue
		}
		if slices.ContainsFunc(commands, func(command string) bool {
			return slices.ContainsFunc(limit.Match, func(match string) bool { return strings.Contains(command, match) })
		}) {
			matched = append(matched, limit)
		}
	}
	slices.SortFunc(matched, func(a, b AdmissionLimit) int { return strings.Compare(a.Class, b.Class) })
	return matched
}
