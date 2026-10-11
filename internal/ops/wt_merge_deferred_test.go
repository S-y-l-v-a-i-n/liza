package ops

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
)

// A merged receipt's deferrals enter the ledger inside the MERGED write, and
// the merge then starts a batch when one is due.
func TestMergeWorktreeRecordsDeferralsAndStartsDueBatch(t *testing.T) {
	recordIndexRefreshes(t, nil)
	var spawned []string
	previous := startDeferredValidationProcess
	startDeferredValidationProcess = func(root string) error {
		spawned = append(spawned, root)
		return nil
	}
	t.Cleanup(func() { startDeferredValidationProcess = previous })

	root, statePath := setupMergeTestRepo(t, "merge-deferred", "coder-1")
	heavySHA := strings.Repeat("7", 64)
	if err := db.New(statePath).Modify(func(state *models.State) error {
		state.Config.DeferredValidation = &models.DeferredValidationConfig{BatchEveryMerges: 1}
		task := state.FindTask("merge-deferred")
		receiptTask := archiveTestTask(task.ID, task.Status, time.Now().UTC())
		receipt := receiptTask.AcceptanceReceipt
		receipt.ReviewCommit = *task.ReviewCommit
		receipt.Deferred = []models.AcceptanceDeferredCommand{{Index: 1, Command: "make long", CommandSHA256: heavySHA, TimeoutSeconds: 600, Authority: models.AcceptanceDeferralContract}}
		task.Validation = []string{"run-tests --all", "make long"}
		task.AcceptanceSource, task.AcceptanceReceipt = receiptTask.AcceptanceSource, receipt
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := MergeWorktree(root, "merge-deferred", "coder-1"); err != nil {
		t.Fatalf("MergeWorktree() error = %v", err)
	}
	state := readStateForTest(t, statePath)
	merged := state.FindTask("merge-deferred")
	ledger := deferredValidationLedger(state)
	if merged.MergeCommit == nil || ledger == nil || len(ledger.Deferrals) != 1 {
		t.Fatalf("ledger after merge = %+v, want one deferral", ledger)
	}
	deferral := ledger.Deferrals[0]
	want := models.DeferredValidationCommand{Command: "make long", CommandSHA256: heavySHA, TimeoutSeconds: 600, Authority: models.AcceptanceDeferralContract}
	if deferral.TaskID != "merge-deferred" || deferral.MergeCommit != *merged.MergeCommit || len(deferral.Commands) != 1 || deferral.Commands[0] != want {
		t.Fatalf("deferral = %+v, want the canonical command at merge %s", deferral, *merged.MergeCommit)
	}
	if runtime.GOOS == "windows" {
		// ADR-0204: triggers start nothing where the runner would refuse.
		if len(spawned) != 0 {
			t.Fatalf("batch starts on windows = %v, want none", spawned)
		}
		return
	}
	if len(spawned) != 1 || spawned[0] != root {
		t.Fatalf("batch starts = %v, want one for %s", spawned, root)
	}
}
