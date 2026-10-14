package ops

import (
	"context"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
)

var valueAdmissionLimit = models.AdmissionLimit{Class: "suite", MaxConcurrent: 1, Match: []string{"value.txt"}}

func newAdmissionDVFixture(t *testing.T) *dvFixture {
	t.Helper()
	shortAdmissionSlotPoll(t)
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	f.merge("task-1", map[string]string{"a.txt": "a\n"}, dvCommand(dvCheckValue, 60))
	f.modify(func(state *models.State) { state.Config.AdmissionLimits = []models.AdmissionLimit{valueAdmissionLimit} })
	return f
}

func TestDeferredValidationProbeWaitsInTheSharedAdmissionPool(t *testing.T) {
	f := newAdmissionDVFixture(t)
	held := holdAdmissionSlots(t, f.root, valueAdmissionLimit)

	type outcome struct {
		result *RunDeferredValidationResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := RunDeferredValidation(context.Background(), f.root, RunDeferredValidationOptions{})
		done <- outcome{result, err}
	}()
	waitForTickets(t, f.root, valueAdmissionLimit.Class, 1)
	select {
	case got := <-done:
		t.Fatalf("runner finished while its probe's class was full: %+v, %v", got.result, got.err)
	default:
	}
	held.release()
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if batch := onlyBatch(t, got.result); batch.Status != models.DeferredValidationBatchPassed {
		t.Fatalf("batch after the slot freed = %+v", batch)
	}
}

func TestDeferredValidationAbortsWhenTheAdmissionClassStaysFull(t *testing.T) {
	f := newAdmissionDVFixture(t)
	shortAdmissionSlotCap(t, 60*time.Second+200*time.Millisecond)
	holdAdmissionSlots(t, f.root, valueAdmissionLimit)

	batch := onlyBatch(t, f.run(false))
	if batch.Status != models.DeferredValidationBatchAborted || batch.Reason != deferredValidationReasonAdmissionSlotWait ||
		len(batch.Results) != 1 || batch.Results[0].Status != models.DeferredValidationResultNotRun {
		t.Fatalf("batch = %+v, want aborted %s with the command not run", batch, deferredValidationReasonAdmissionSlotWait)
	}
	if f.ledger().Runner != nil {
		t.Fatal("aborted batch left its runner record")
	}
}
