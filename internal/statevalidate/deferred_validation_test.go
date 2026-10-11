package statevalidate

import (
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/referencecontract"
)

func deferredReceiptTask() models.Task {
	task := archivedReceiptTask(models.TaskStatusReadyForReview)
	task.Archived = nil
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	task.AcceptanceReceipt = &models.AcceptanceReceipt{
		Version: 1, ReviewCommit: *task.ReviewCommit, Source: *task.AcceptanceSource,
		ManifestPath: "m.json", ManifestBlob: strings.Repeat("5", 40),
		Mappings: []referencecontract.AcceptanceMapping{{ObligationID: "AC-1"}},
		Commands: []models.AcceptanceCommandResult{{CommandSHA256: strings.Repeat("6", 64), StartedAt: now, FinishedAt: now}},
		Deferred: []models.AcceptanceDeferredCommand{
			{Index: 1, Command: "make long", CommandSHA256: strings.Repeat("7", 64), TimeoutSeconds: 60, Authority: models.AcceptanceDeferralContract},
			{Index: 2, Command: "make longer", CommandSHA256: strings.Repeat("8", 64), TimeoutSeconds: 60, Authority: models.AcceptanceDeferralOperator},
		},
	}
	return task
}

func TestValidateAcceptanceStateReceiptDeferrals(t *testing.T) {
	valid := deferredReceiptTask()
	if err := acceptanceStateErr(&valid); err != nil {
		t.Fatalf("valid deferred receipt rejected: %v", err)
	}
	cases := map[string]func(*models.AcceptanceReceipt){
		"unordered":     func(r *models.AcceptanceReceipt) { r.Deferred[1].Index = 1 },
		"negative":      func(r *models.AcceptanceReceipt) { r.Deferred[0].Index = -1 },
		"short digest":  func(r *models.AcceptanceReceipt) { r.Deferred[0].CommandSHA256 = "abc" },
		"no timeout":    func(r *models.AcceptanceReceipt) { r.Deferred[0].TimeoutSeconds = 0 },
		"bad authority": func(r *models.AcceptanceReceipt) { r.Deferred[0].Authority = "reviewer" },
		"over bound": func(r *models.AcceptanceReceipt) {
			for len(r.Commands)+len(r.Deferred) <= 64 {
				r.Commands = append(r.Commands, r.Commands[0])
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			task := deferredReceiptTask()
			mutate(task.AcceptanceReceipt)
			if err := acceptanceStateErr(&task); err == nil {
				t.Fatal("invalid receipt deferral accepted")
			}
		})
	}
}

func validDeferredLedger() *models.DeferredValidationLedger {
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	command := models.DeferredValidationCommand{Command: "make long", CommandSHA256: strings.Repeat("7", 64), TimeoutSeconds: 60, Authority: models.AcceptanceDeferralContract}
	return &models.DeferredValidationLedger{
		Deferrals: []models.DeferredValidationDeferral{
			{TaskID: "coding-1", MergeCommit: strings.Repeat("a", 40), RecordedAt: now, Commands: []models.DeferredValidationCommand{command}},
		},
		Retirements: []models.DeferredValidationRetirement{
			{Command: command.Command, CommandSHA256: command.CommandSHA256, Ruling: "decision D-97", RecordedAt: now, CoversDeferrals: 1},
		},
		Batches: []models.DeferredValidationBatch{
			{ID: 1, Commit: strings.Repeat("a", 40), StartedAt: now, FinishedAt: now, Status: models.DeferredValidationBatchFailed,
				Results:  []models.DeferredValidationResult{{CommandSHA256: command.CommandSHA256, Status: models.DeferredValidationResultFailed}},
				Culprits: []models.DeferredValidationCulprit{{CommandSHA256: command.CommandSHA256, TaskID: "coding-1"}}},
		},
		Runner: &models.DeferredValidationRunner{Token: "t", Commit: strings.Repeat("a", 40), StartedAt: now},
	}
}

func TestDeferredValidationLedgerShape(t *testing.T) {
	state := validIntegrationState(t)
	state.Goal.Integration.DeferredValidation = validDeferredLedger()
	if err := integrationLifecycleErr(state, "", false); err != nil {
		t.Fatalf("valid ledger rejected: %v", err)
	}
	cases := map[string]struct {
		mutate func(*models.DeferredValidationLedger)
		want   string
	}{
		"short merge commit":   {func(l *models.DeferredValidationLedger) { l.Deferrals[0].MergeCommit = "abc" }, "deferrals[0]"},
		"command authority":    {func(l *models.DeferredValidationLedger) { l.Deferrals[0].Commands[0].Authority = "" }, "deferrals[0].commands[0]"},
		"retirement overreach": {func(l *models.DeferredValidationLedger) { l.Retirements[0].CoversDeferrals = 2 }, "retirements[0]"},
		"retirement no ruling": {func(l *models.DeferredValidationLedger) { l.Retirements[0].Ruling = " " }, "retirements[0]"},
		"batch id order": {func(l *models.DeferredValidationLedger) {
			l.Batches = append(l.Batches, l.Batches[0])
		}, "strictly increasing"},
		"batch status":      {func(l *models.DeferredValidationLedger) { l.Batches[0].Status = "green" }, "valid status"},
		"result status":     {func(l *models.DeferredValidationLedger) { l.Batches[0].Results[0].Status = "red" }, "results[0]"},
		"culprit ambiguous": {func(l *models.DeferredValidationLedger) { l.Batches[0].Culprits[0].Unattributed = true }, "culprits[0]"},
		"runner token":      {func(l *models.DeferredValidationLedger) { l.Runner.Token = "" }, "runner"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			state := validIntegrationState(t)
			state.Goal.Integration.DeferredValidation = validDeferredLedger()
			tc.mutate(state.Goal.Integration.DeferredValidation)
			assertIntegrationErrorContains(t, integrationLifecycleErr(state, "", false), tc.want)
		})
	}
}

func TestDeferredValidationLedgerTransition(t *testing.T) {
	withLedger := func(ledger *models.DeferredValidationLedger) *models.State {
		state := validIntegrationState(t)
		state.Goal.Integration.DeferredValidation = ledger
		return state
	}
	previous := withLedger(validDeferredLedger())

	t.Run("first ledger and appends are valid", func(t *testing.T) {
		if err := ValidateIntegrationLifecycleTransition(withLedger(nil), previous); err != nil {
			t.Fatalf("first ledger refused: %v", err)
		}
		empty := withLedger(&models.DeferredValidationLedger{})
		if err := ValidateIntegrationLifecycleTransition(empty, previous); err != nil {
			t.Fatalf("append onto empty lists refused: %v", err)
		}
		candidate := cloneIntegrationState(t, previous)
		ledger := candidate.Goal.Integration.DeferredValidation
		ledger.Deferrals = append(ledger.Deferrals, ledger.Deferrals[0])
		next := ledger.Batches[0]
		next.ID = 2
		ledger.Batches = append(ledger.Batches, next)
		ledger.Runner = nil
		if err := ValidateIntegrationLifecycleTransition(previous, candidate); err != nil {
			t.Fatalf("append-only transition refused: %v", err)
		}
	})

	cases := map[string]struct {
		mutate func(*models.DeferredValidationLedger) *models.DeferredValidationLedger
		want   string
	}{
		"cleared": {func(*models.DeferredValidationLedger) *models.DeferredValidationLedger { return nil }, "cannot be cleared"},
		"deferral removed": {func(l *models.DeferredValidationLedger) *models.DeferredValidationLedger {
			l.Deferrals = nil
			return l
		}, "deferrals are append-only"},
		"retirement rewritten": {func(l *models.DeferredValidationLedger) *models.DeferredValidationLedger {
			l.Retirements[0].Ruling = "other"
			return l
		}, "retirements are append-only"},
		"batch edited": {func(l *models.DeferredValidationLedger) *models.DeferredValidationLedger {
			l.Batches[0].Status = models.DeferredValidationBatchPassed
			return l
		}, "batch 1 cannot be edited"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := cloneIntegrationState(t, previous)
			candidate.Goal.Integration.DeferredValidation = tc.mutate(candidate.Goal.Integration.DeferredValidation)
			assertIntegrationErrorContains(t, ValidateIntegrationLifecycleTransition(previous, candidate), tc.want)
		})
	}

	// Retention prunes old batches and always keeps the newest, so a pruned
	// identity sits below the last kept one and cannot come back.
	t.Run("pruned batch cannot be re-added", func(t *testing.T) {
		full := cloneIntegrationState(t, previous)
		ledger := full.Goal.Integration.DeferredValidation
		next := ledger.Batches[0]
		next.ID = 2
		ledger.Batches = append(ledger.Batches, next)
		pruned := cloneIntegrationState(t, full)
		pruned.Goal.Integration.DeferredValidation.Batches = pruned.Goal.Integration.DeferredValidation.Batches[1:]
		if err := ValidateIntegrationLifecycleTransition(full, pruned); err != nil {
			t.Fatalf("pruning refused: %v", err)
		}
		assertIntegrationErrorContains(t, ValidateIntegrationLifecycleTransition(pruned, full), "batch 1 cannot be edited or re-added")
	})

	// With the high-water identity, even pruning every batch cannot free an
	// identity for reuse.
	t.Run("high-water identity survives pruning everything", func(t *testing.T) {
		withHighWater := cloneIntegrationState(t, previous)
		withHighWater.Goal.Integration.DeferredValidation.LastBatchID = 1
		empty := cloneIntegrationState(t, withHighWater)
		empty.Goal.Integration.DeferredValidation.Batches = nil
		if err := ValidateIntegrationLifecycleTransition(withHighWater, empty); err != nil {
			t.Fatalf("pruning refused: %v", err)
		}
		assertIntegrationErrorContains(t, ValidateIntegrationLifecycleTransition(empty, withHighWater), "batch 1 cannot be edited or re-added")
		lowered := cloneIntegrationState(t, empty)
		lowered.Goal.Integration.DeferredValidation.LastBatchID = 0
		assertIntegrationErrorContains(t, ValidateIntegrationLifecycleTransition(empty, lowered), "cannot decrease")
	})
}
