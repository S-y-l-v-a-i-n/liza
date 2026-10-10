package ops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/git"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// setupSubmittedAcceptanceTask turns the acceptance scenario into a submitted
// task with no acceptance receipt. With orphan set, its commits are first
// moved onto a commit that integration then rolls back (D-26).
func setupSubmittedAcceptanceTask(t *testing.T, orphan bool) (string, string, string) {
	t.Helper()
	root, taskID, commit, _, bb := setupAcceptanceScenario(t)
	wt := git.New(root).GetWorktreePath(taskID)
	head, base := commit, testhelpers.MustGit(t, root, "merge-base", commit, "integration")
	orphanCommit := ""
	if orphan {
		orphanCommit, head = orphanWorktreeBase(t, root, wt)
		base = orphanCommit
	}
	if err := bb.Modify(func(state *models.State) error {
		registerClaimReviewerTaskTestAgents(state)
		task := state.FindTask(taskID)
		task.Status = models.TaskStatusReadyForReview
		task.LeaseExpires = nil
		task.ReviewCommit = &head
		task.BaseCommit = &base
		task.AcceptanceReceipt = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return root, taskID, orphanCommit
}

// orphanWorktreeBase rebases the worktree onto a transient integration commit,
// then rolls integration back past it and moves on. It returns the orphaned
// commit and the new worktree HEAD.
func orphanWorktreeBase(t *testing.T, root, wt string) (string, string) {
	t.Helper()
	testhelpers.MustGit(t, root, "checkout", "integration")
	before := testhelpers.MustGit(t, root, "rev-parse", "integration")
	if err := os.WriteFile(filepath.Join(root, "transient.txt"), []byte("transient merge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, root, "add", "transient.txt")
	testhelpers.MustGit(t, root, "commit", "-m", "transient merge")
	orphan := testhelpers.MustGit(t, root, "rev-parse", "HEAD")
	testhelpers.MustGit(t, wt, "rebase", "integration")
	head := testhelpers.MustGit(t, wt, "rev-parse", "HEAD")
	testhelpers.MustGit(t, root, "reset", "--hard", before)
	if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, root, "add", "unrelated.txt")
	testhelpers.MustGit(t, root, "commit", "-m", "integration moves on")
	return orphan, head
}

func claimCandidateFailure(t *testing.T, root, taskID string) ReviewClaimCandidateFailure {
	t.Helper()
	_, err := ClaimReviewerTask(ClaimReviewerTaskInput{ProjectRoot: root, AgentID: "code-reviewer-1", LeaseDuration: 1800})
	var failure *ReviewClaimFailure
	if !errors.As(err, &failure) {
		t.Fatalf("ClaimReviewerTask() error = %v, want a typed candidate failure", err)
	}
	for _, candidate := range failure.Candidates {
		if candidate.TaskID == taskID {
			return candidate
		}
	}
	t.Fatalf("candidates = %#v, want a record for %s", failure.Candidates, taskID)
	return ReviewClaimCandidateFailure{}
}

// An orphaned base is invalid whatever the evidence says, and evidence is
// regenerated on the repaired boundary. Classifying it as an acceptance
// failure hides it from the boundary repair and quarantines it forever.
func TestReviewClaimClassifiesOrphanedBaseBeforeAcceptanceEvidence(t *testing.T) {
	t.Parallel()
	root, taskID, orphan := setupSubmittedAcceptanceTask(t, true)
	candidate := claimCandidateFailure(t, root, taskID)
	if candidate.Class != ReviewClaimClassReviewBoundaryRepair {
		t.Fatalf("class = %q, want %q for an orphaned base", candidate.Class, ReviewClaimClassReviewBoundaryRepair)
	}
	if !strings.Contains(candidate.Recovery, "rebase --onto") || !strings.Contains(candidate.Recovery, orphan) {
		t.Errorf("recovery = %q, want the rebase --onto repair naming orphan %s", candidate.Recovery, orphan)
	}
}

// Guard: without an orphan, a missing receipt stays an acceptance failure.
func TestReviewClaimKeepsAcceptanceEvidenceClassWithoutOrphan(t *testing.T) {
	t.Parallel()
	root, taskID, _ := setupSubmittedAcceptanceTask(t, false)
	candidate := claimCandidateFailure(t, root, taskID)
	if candidate.Class != ReviewClaimClassAcceptanceEvidence {
		t.Fatalf("class = %q, want %q", candidate.Class, ReviewClaimClassAcceptanceEvidence)
	}
}
