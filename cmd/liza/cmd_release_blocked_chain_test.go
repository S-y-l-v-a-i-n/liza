package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/brand"
	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/ops"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// setupReleaseBlockedChainCLI leaves c BLOCKED at the blocked-recovery cap
// (a -> b -> c, each replaced while BLOCKED) through the real supersession.
func setupReleaseBlockedChainCLI(t *testing.T) string {
	t.Helper()
	resetRootCmdForTest(t)
	t.Setenv(brand.EnvName("AGENT_ID"), "")
	t.Setenv(brand.LegacyEnvName("AGENT_ID"), "")
	root := t.TempDir()
	testhelpers.SetupTestGitRepo(t, root)
	statePath, _ := testhelpers.SetupLizaDir(t, root)
	state := testhelpers.CreateValidState()
	state.Tasks = nil
	testhelpers.WriteInitialState(t, statePath, state)

	clock := time.Now().UTC().Add(-time.Hour)
	block := func(id string) {
		t.Helper()
		clock = clock.Add(time.Minute)
		if err := db.New(statePath).Modify(func(s *models.State) error {
			task := s.FindTask(id)
			if task == nil {
				s.Tasks = append(s.Tasks, testhelpers.BuildTaskByStatus(id, models.TaskStatusReady, clock))
				task = &s.Tasks[len(s.Tasks)-1]
			}
			reason := id + " blocked"
			task.Status, task.BlockedReason = models.TaskStatusBlocked, &reason
			task.History = append(task.History, models.TaskHistoryEntry{Time: clock, Event: models.TaskEventBlocked})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	replace := func(id, replacement string) {
		t.Helper()
		if err := db.New(statePath).Modify(func(s *models.State) error {
			s.Tasks = append(s.Tasks, testhelpers.BuildTaskByStatus(replacement, models.TaskStatusReady, clock))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ops.SupersedeTaskWithOptions(root, id, []string{replacement}, "replace blocked task", "orchestrator-1",
			ops.SupersedeTaskOptions{Changed: "new approach for " + replacement}); err != nil {
			t.Fatalf("supersede %s: %v", id, err)
		}
	}
	block("a")
	replace("a", "b")
	block("b")
	replace("b", "c")
	block("c")
	return root
}

func TestReleaseBlockedChainCLI(t *testing.T) {
	root := setupReleaseBlockedChainCLI(t)
	stdout, err := executeRootCommandCapture(t, root, "release-blocked-chain", "c", "--reason", "human ruled one more replacement", "--json")
	if err != nil {
		t.Fatal(err)
	}
	envelope := parseEnvelope(t, stdout)
	result, _ := envelope["result"].(map[string]any)
	if envelope["ok"] != true || result["task_id"] != "c" || result["blocked_at"] == nil {
		t.Fatalf("missing success envelope: %s", stdout)
	}
	state, err := db.New(paths.New(root).StatePath()).Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, capped := models.BlockedRecoveryCapped(state, state.FindTask("c")); capped {
		t.Fatal("released task still capped")
	}
	if state.Config.Mode != models.SystemModeRunning {
		t.Fatalf("mode = %s, want RUNNING", state.Config.Mode)
	}
}

func TestReleaseBlockedChainCLIRefusals(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		agent    bool
		fragment string
	}{
		{name: "agent identity", agent: true, args: []string{"release-blocked-chain", "c", "--reason", "r", "--json"}, fragment: "operator-only"},
		{name: "missing reason", args: []string{"release-blocked-chain", "c"}, fragment: "required flag"},
		{name: "not capped", args: []string{"release-blocked-chain", "a", "--reason", "r", "--json"}, fragment: "not BLOCKED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupReleaseBlockedChainCLI(t)
			if tc.agent {
				t.Setenv(brand.EnvName("AGENT_ID"), "orchestrator-1")
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
