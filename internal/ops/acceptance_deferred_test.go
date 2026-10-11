package ops

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/git"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/referencecontract"
	"github.com/liza-mas/liza/internal/testhelpers"
)

const deferredHeavyCommand = "sh heavy_test.sh"

// deferredAcceptanceScenario extends the complete acceptance scenario with a
// second, regression-only canonical command. The command records an absolute
// marker when it runs, so a test can prove admission skipped it. deferred is
// the contract's reviewed deferred list.
func deferredAcceptanceScenario(t *testing.T, deferred []int) (root, taskID, commit, agentID, marker string, bb *db.Blackboard) {
	t.Helper()
	root, taskID, _, agentID, bb = completeAcceptanceScenario(t)
	marker = filepath.Join(t.TempDir(), "heavy-ran")
	validation := []string{"sh boundary_test.sh", deferredHeavyCommand}

	// The reviewed parent allocates both commands; re-point its review at
	// the amended carrier so the allocation stays independently approved.
	planPath := filepath.Join(root, "specs/acceptance-plan.md")
	plan, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	contract := `"validation":["sh boundary_test.sh","` + deferredHeavyCommand + `"]`
	if len(deferred) > 0 {
		indices := make([]string, len(deferred))
		for i, index := range deferred {
			indices[i] = strconv.Itoa(index)
		}
		contract += `,"deferred":[` + strings.Join(indices, ",") + `]`
	}
	amended := strings.Replace(string(plan), `"validation":["sh boundary_test.sh"]`, contract, 1)
	if amended == string(plan) {
		t.Fatal("fixture plan no longer carries the expected validation list")
	}
	if err := os.WriteFile(planPath, []byte(amended), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, root, "add", "specs/acceptance-plan.md")
	testhelpers.MustGit(t, root, "commit", "-m", "test: allocate a regression-only command")
	parentCommit := testhelpers.MustGit(t, root, "rev-parse", "HEAD")

	wt := git.New(root).GetWorktreePath(taskID)
	testhelpers.MustGit(t, wt, "rebase", "integration")
	script := "set -eu\n: > " + strconv.Quote(marker) + "\nprintf 'PASS heavy regression\\n'\n"
	if err := os.WriteFile(filepath.Join(wt, "heavy_test.sh"), []byte(script), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, wt, "add", "heavy_test.sh")
	testhelpers.MustGit(t, wt, "commit", "-m", "test: add the regression-only suite")
	commit = testhelpers.MustGit(t, wt, "rev-parse", "HEAD")
	if err := bb.Modify(func(state *models.State) error {
		task := state.FindTask(taskID)
		task.Validation = validation
		task.BaseCommit = &parentCommit
		parent := state.FindTask("acceptance-parent")
		parent.ReviewCommit, parent.MergeCommit = &parentCommit, &parentCommit
		parent.Output[0].Validation = validation
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return root, taskID, commit, agentID, marker, bb
}

func requireMarkerAbsent(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("admission ran the deferred command (marker stat err = %v)", err)
	}
}

func requireSingleDeferral(t *testing.T, receipt *models.AcceptanceReceipt, index int, authority models.AcceptanceDeferralAuthority) {
	t.Helper()
	if receipt == nil || len(receipt.Deferred) != 1 || len(receipt.Commands) != 1 {
		t.Fatalf("receipt = %#v, want one executed and one deferred command", receipt)
	}
	got := receipt.Deferred[0]
	if got.Index != index || got.Authority != authority || got.CommandSHA256 == "" || got.TimeoutSeconds != 10 {
		t.Fatalf("deferral = %#v, want index %d with %s authority", got, index, authority)
	}
}

func TestAcceptanceDeferredContractCommandSkippedAndCertified(t *testing.T) {
	t.Parallel()
	root, taskID, commit, agentID, marker, bb := deferredAcceptanceScenario(t, []int{1})
	result, err := SubmitForReview(root, taskID, commit, agentID)
	if err != nil {
		t.Fatal(err)
	}
	requireMarkerAbsent(t, marker)
	if len(result.DeferredCommands) != 1 || result.DeferredCommands[0].Command != deferredHeavyCommand {
		t.Fatalf("submission result deferred = %+v, want the skipped command reported", result.DeferredCommands)
	}
	state := readAcceptanceState(t, bb)
	task := state.FindTask(taskID)
	requireSingleDeferral(t, task.AcceptanceReceipt, 1, models.AcceptanceDeferralContract)
	if task.AcceptanceReceipt.Commands[0].Output != "PASS identity assertion\n" {
		t.Fatalf("executed command = %#v, want the mapped proof", task.AcceptanceReceipt.Commands[0])
	}
	if err := validateAcceptanceForAssignment(root, state, task); err != nil {
		t.Fatalf("receipt certifying a contract deferral is not reviewable: %v", err)
	}

	for name, tamper := range map[string]func(*models.AcceptanceReceipt){
		"withdrawn": func(r *models.AcceptanceReceipt) { r.Deferred = nil },
		"authority": func(r *models.AcceptanceReceipt) { r.Deferred[0].Authority = models.AcceptanceDeferralOperator },
		"identity":  func(r *models.AcceptanceReceipt) { r.Deferred[0].CommandSHA256 = strings.Repeat("0", 64) },
		"index":     func(r *models.AcceptanceReceipt) { r.Deferred[0].Index = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			tampered := readAcceptanceState(t, bb)
			tamperedTask := tampered.FindTask(taskID)
			tamper(tamperedTask.AcceptanceReceipt)
			if err := validateAcceptanceForAssignment(root, tampered, tamperedTask); err == nil {
				t.Fatal("assignment accepted a tampered deferral")
			}
		})
	}
}

func TestAcceptanceDeferredOperatorRuling(t *testing.T) {
	t.Parallel()
	root, taskID, commit, agentID, marker, bb := deferredAcceptanceScenario(t, nil)
	if _, err := RuleDeferredValidation(root, deferredHeavyCommand, "decision D-97"); err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitForReview(root, taskID, commit, agentID); err != nil {
		t.Fatal(err)
	}
	requireMarkerAbsent(t, marker)
	state := readAcceptanceState(t, bb)
	task := state.FindTask(taskID)
	requireSingleDeferral(t, task.AcceptanceReceipt, 1, models.AcceptanceDeferralOperator)
	if err := validateAcceptanceForAssignment(root, state, task); err != nil {
		t.Fatalf("receipt certifying an operator deferral is not reviewable: %v", err)
	}

	// Withdrawing the ruling withdraws the authority the receipt relied on.
	if _, err := UnruleDeferredValidation(root, deferredHeavyCommand); err != nil {
		t.Fatal(err)
	}
	state = readAcceptanceState(t, bb)
	err := validateAcceptanceForAssignment(root, state, state.FindTask(taskID))
	requireAcceptanceError(t, err, taskID)
	if !strings.Contains(err.Error(), "deferred") {
		t.Fatalf("refusal = %v, want it to name the withdrawn deferral", err)
	}
}

// A ruling recorded after submission never strands a receipt that executed
// the command: an execution is stronger evidence than a deferral.
func TestAcceptanceDeferredRulingAfterSubmitKeepsExecutedReceipt(t *testing.T) {
	t.Parallel()
	root, taskID, commit, agentID, marker, bb := deferredAcceptanceScenario(t, nil)
	if _, err := SubmitForReview(root, taskID, commit, agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("undeferred command did not run at admission: %v", err)
	}
	if _, err := RuleDeferredValidation(root, deferredHeavyCommand, "decision D-97"); err != nil {
		t.Fatal(err)
	}
	state := readAcceptanceState(t, bb)
	task := state.FindTask(taskID)
	if len(task.AcceptanceReceipt.Commands) != 2 || len(task.AcceptanceReceipt.Deferred) != 0 {
		t.Fatalf("receipt = %#v, want both commands executed", task.AcceptanceReceipt)
	}
	if err := validateAcceptanceForAssignment(root, state, task); err != nil {
		t.Fatalf("a later ruling stranded an executed receipt: %v", err)
	}
}

// An operator ruling may defer a mapped command (the I-785 authority): the
// batch receipt then satisfies its done_when. The contract cannot.
func TestAcceptanceDeferredOperatorRulingOnMappedCommand(t *testing.T) {
	t.Parallel()
	root, taskID, commit, agentID, marker, bb := deferredAcceptanceScenario(t, nil)
	if _, err := RuleDeferredValidation(root, "sh boundary_test.sh", "decision D-97"); err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitForReview(root, taskID, commit, agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the executable command did not run: %v", err)
	}
	state := readAcceptanceState(t, bb)
	task := state.FindTask(taskID)
	requireSingleDeferral(t, task.AcceptanceReceipt, 0, models.AcceptanceDeferralOperator)
	if err := validateAcceptanceForAssignment(root, state, task); err != nil {
		t.Fatalf("operator deferral of a mapped command is not reviewable: %v", err)
	}
}

// A failure names the command's canonical index, not its position among the
// commands that ran.
func TestAcceptanceDeferredFailureNamesCanonicalIndex(t *testing.T) {
	t.Parallel()
	root, taskID, _, agentID, _, _ := deferredAcceptanceScenario(t, nil)
	if _, err := RuleDeferredValidation(root, "sh boundary_test.sh", "decision D-97"); err != nil {
		t.Fatal(err)
	}
	wt := git.New(root).GetWorktreePath(taskID)
	if err := os.WriteFile(filepath.Join(wt, "heavy_test.sh"), []byte("exit 5\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, wt, "commit", "-am", "test: break the regression suite")
	commit := testhelpers.MustGit(t, wt, "rev-parse", "HEAD")
	_, err := SubmitForReview(root, taskID, commit, agentID)
	if err == nil || !strings.Contains(err.Error(), "acceptance.execution[1]") {
		t.Fatalf("err = %v, want the failure at canonical index 1", err)
	}
}

// Planning preflight refuses a declaration deferring a runtime-input command
// before the child is ever allocated.
func TestPlanningOutputAcceptanceRefusesDeferredRuntimeInputCommand(t *testing.T) {
	t.Parallel()
	root, taskID, _, _, bb := setupPlanningAcceptanceSubmission(t, "", false)
	wt := git.New(root).GetWorktreePath(taskID)
	path := filepath.Join(wt, "specs/plans/child.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	deferring := strings.Replace(string(content), `"node --test tests/future.test.mjs"]`, `"node --test tests/future.test.mjs"],"deferred":[1]`, 1)
	if err := os.WriteFile(path, []byte(deferring), 0644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, wt, "commit", "-am", "Defer the long suite")
	commit := testhelpers.MustGit(t, wt, "rev-parse", "HEAD")
	task := readAcceptanceState(t, bb).FindTask(taskID)
	if err := validatePlanningOutputAcceptance(root, task, commit); err != nil {
		t.Fatalf("deferring a regression-only command refused: %v", err)
	}
	task.Output[0].RuntimeInputs = []models.RuntimeInput{{ID: "w03", Commands: []string{"node --test tests/future.test.mjs"}}}
	err = validatePlanningOutputAcceptance(root, task, commit)
	requireAcceptanceErrorField(t, err, "output[0].acceptance")
	testhelpers.RequireErrorContains(t, err, "runtime input")
}

func requireAcceptanceErrorField(t *testing.T, err error, field string) {
	t.Helper()
	var evidenceErr *AcceptanceEvidenceError
	if !errors.As(err, &evidenceErr) || evidenceErr.Field != field {
		t.Fatalf("err = %v, want an acceptance refusal on %s", err, field)
	}
}

func TestAcceptanceDeferralsRuntimeInputRunsInGate(t *testing.T) {
	t.Parallel()
	runtimeInputs := []models.RuntimeInput{{Commands: []string{deferredHeavyCommand}}}
	contract := &referencecontract.AcceptanceContract{Validation: []string{"sh boundary_test.sh", deferredHeavyCommand}, TimeoutSeconds: 10, Deferred: []int{1}}
	if _, err := acceptanceDeferrals(models.Config{}, contract, runtimeInputs); err == nil || !strings.Contains(err.Error(), "runtime input") {
		t.Fatalf("contract deferral of a runtime-input command = %v, want refusal", err)
	}

	contract.Deferred = nil
	config := models.Config{DeferredValidation: &models.DeferredValidationConfig{Rulings: []models.DeferredValidationRuling{
		{Command: deferredHeavyCommand, Ruling: "decision D-97"},
		{Command: "sh boundary_test.sh", Ruling: "decision D-97"},
	}}}
	deferred, err := acceptanceDeferrals(config, contract, runtimeInputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(deferred) != 1 || deferred[0].Index != 0 || deferred[0].Authority != models.AcceptanceDeferralOperator {
		t.Fatalf("deferrals = %#v, want the ruling to skip only the non-runtime-input command", deferred)
	}
}
