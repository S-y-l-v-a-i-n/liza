package models

import (
	"strings"
	"testing"
)

func TestValidateAdmissionLimit(t *testing.T) {
	valid := AdmissionLimit{Class: "db-suite", MaxConcurrent: 2, Match: []string{"--decision-model-db"}}
	if err := ValidateAdmissionLimit(valid); err != nil {
		t.Fatalf("valid limit refused: %v", err)
	}
	for name, mutate := range map[string]func(*AdmissionLimit){
		"path class":      func(l *AdmissionLimit) { l.Class = "../x" },
		"upper class":     func(l *AdmissionLimit) { l.Class = "DB" },
		"zero max":        func(l *AdmissionLimit) { l.MaxConcurrent = 0 },
		"huge max":        func(l *AdmissionLimit) { l.MaxConcurrent = MaxAdmissionLimitConcurrent + 1 },
		"no match":        func(l *AdmissionLimit) { l.Match = nil },
		"empty match":     func(l *AdmissionLimit) { l.Match = []string{""} },
		"multiline match": func(l *AdmissionLimit) { l.Match = []string{"a\nb"} },
		"long match":      func(l *AdmissionLimit) { l.Match = []string{strings.Repeat("x", MaxAdmissionLimitMatchBytes+1)} },
	} {
		limit := valid
		limit.Match = append([]string(nil), valid.Match...)
		mutate(&limit)
		if ValidateAdmissionLimit(limit) == nil {
			t.Errorf("%s: accepted %+v", name, limit)
		}
	}
}

func TestAdmissionLimitsForMatchesSortsAndSkipsInvalid(t *testing.T) {
	config := Config{AdmissionLimits: []AdmissionLimit{
		{Class: "web", MaxConcurrent: 1, Match: []string{"playwright"}},
		{Class: "db", MaxConcurrent: 2, Match: []string{"--db", "pg_"}},
		{Class: "../bad", MaxConcurrent: 1, Match: []string{"go test"}},
		{Class: "db", MaxConcurrent: 9, Match: []string{"go test"}},
		{Class: "cpu", MaxConcurrent: 1, Match: []string{"bench"}},
	}}
	got := AdmissionLimitsFor(config, []string{"npx playwright test", "go test ./... --db"})
	if len(got) != 2 || got[0].Class != "db" || got[0].MaxConcurrent != 2 || got[1].Class != "web" {
		t.Fatalf("matched = %+v, want db (first definition) then web", got)
	}
	if got := AdmissionLimitsFor(config, []string{"make lint"}); got != nil {
		t.Fatalf("unmatched commands got %+v", got)
	}
	if got := AdmissionLimitsFor(Config{}, []string{"go test --db"}); got != nil {
		t.Fatalf("no configured limits got %+v", got)
	}
}
