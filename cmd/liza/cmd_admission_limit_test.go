package main

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/brand"
	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func setupAdmissionLimitCLI(t *testing.T) string {
	t.Helper()
	resetRootCmdForTest(t)
	t.Setenv(brand.EnvName("AGENT_ID"), "")
	t.Setenv(brand.LegacyEnvName("AGENT_ID"), "")
	root := t.TempDir()
	testhelpers.SetupTestGitRepo(t, root)
	statePath, _ := testhelpers.SetupLizaDir(t, root)
	testhelpers.WriteInitialState(t, statePath, testhelpers.CreateValidState())
	return root
}

func admissionLimitsIn(t *testing.T, root string) []models.AdmissionLimit {
	t.Helper()
	state, err := db.New(paths.New(root).StatePath()).Read()
	if err != nil {
		t.Fatal(err)
	}
	return state.Config.AdmissionLimits
}

func admissionLimitOutcome(t *testing.T, root string, args ...string) string {
	t.Helper()
	resetRootCmdForTest(t)
	stdout, err := executeRootCommandCapture(t, root, append(args, "--json")...)
	if err != nil {
		t.Fatalf("%v: %v (%s)", args, err, stdout)
	}
	result, _ := parseEnvelope(t, stdout)["result"].(map[string]any)
	outcome, _ := result["outcome"].(string)
	return outcome
}

func TestAdmissionLimitCLILifecycle(t *testing.T) {
	root := setupAdmissionLimitCLI(t)
	set := []string{"admission-limit", "set", "db-suite", "--max-concurrent", "2", "--match=--decision-model-db", "--match", "pg_suite"}
	if got := admissionLimitOutcome(t, root, set...); got != "set" {
		t.Fatalf("first set outcome = %q", got)
	}
	want := models.AdmissionLimit{Class: "db-suite", MaxConcurrent: 2, Match: []string{"--decision-model-db", "pg_suite"}}
	if limits := admissionLimitsIn(t, root); len(limits) != 1 || limits[0].Class != want.Class || limits[0].MaxConcurrent != 2 || !slices.Equal(limits[0].Match, want.Match) {
		t.Fatalf("stored limits = %+v", limits)
	}
	if got := admissionLimitOutcome(t, root, set...); got != "unchanged" {
		t.Fatalf("identical set outcome = %q", got)
	}

	resetRootCmdForTest(t)
	stdout, err := executeRootCommandCapture(t, root, "admission-limit", "set", "db-suite", "--max-concurrent", "1", "--match=--decision-model-db", "--json")
	if err == nil || !strings.Contains(stdout+err.Error(), "--replace") {
		t.Fatalf("different definition without --replace = %s, %v", stdout, err)
	}
	if got := admissionLimitOutcome(t, root, "admission-limit", "set", "db-suite", "--max-concurrent", "1", "--match=--decision-model-db", "--replace", "--reason", "DB host saturated"); got != "replaced" {
		t.Fatalf("replace outcome = %q", got)
	}
	if limits := admissionLimitsIn(t, root); len(limits) != 1 || limits[0].MaxConcurrent != 1 {
		t.Fatalf("replaced limits = %+v", limits)
	}

	resetRootCmdForTest(t)
	stdout, err = executeRootCommandCapture(t, root, "get", "config.admission_limits", "--json")
	if err != nil || !strings.Contains(stdout, "db-suite") {
		t.Fatalf("get config.admission_limits = %s, %v", stdout, err)
	}

	if got := admissionLimitOutcome(t, root, "admission-limit", "remove", "db-suite", "--reason", "suites isolated"); got != "removed" {
		t.Fatalf("remove outcome = %q", got)
	}
	if got := admissionLimitOutcome(t, root, "admission-limit", "remove", "db-suite", "--reason", "again"); got != "unchanged" {
		t.Fatalf("second remove outcome = %q", got)
	}
	if limits := admissionLimitsIn(t, root); limits != nil {
		t.Fatalf("limits after remove = %+v", limits)
	}
}

func TestAdmissionLimitCLIRefusals(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		agent    bool
		fragment string
	}{
		{name: "agent identity", agent: true, args: []string{"admission-limit", "set", "db", "--max-concurrent", "1", "--match", "x", "--json"}, fragment: "operator-only"},
		{name: "unsafe class", args: []string{"admission-limit", "set", "../db", "--max-concurrent", "1", "--match", "x", "--json"}, fragment: "must match"},
		{name: "zero slots", args: []string{"admission-limit", "set", "db", "--max-concurrent", "0", "--match", "x", "--json"}, fragment: "max_concurrent"},
		{name: "missing match", args: []string{"admission-limit", "set", "db", "--max-concurrent", "1"}, fragment: "required flag"},
		{name: "remove without reason", args: []string{"admission-limit", "remove", "db"}, fragment: "required flag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupAdmissionLimitCLI(t)
			if tc.agent {
				t.Setenv(brand.EnvName("AGENT_ID"), "coder-1")
			}
			before, err := os.ReadFile(paths.New(root).StatePath())
			if err != nil {
				t.Fatal(err)
			}
			stdout, err := executeRootCommandCapture(t, root, tc.args...)
			if err == nil {
				t.Fatalf("refusal case accepted: %s", stdout)
			}
			if !strings.Contains(stdout+err.Error(), tc.fragment) {
				t.Fatalf("refusal %q / %v does not name %q", stdout, err, tc.fragment)
			}
			after, err := os.ReadFile(paths.New(root).StatePath())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("refusal changed state")
			}
		})
	}
}
