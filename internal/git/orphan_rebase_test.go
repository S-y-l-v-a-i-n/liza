package git

import (
	stderrors "errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/liza-mas/liza/internal/testhelpers"
)

func commitFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, dir, "add", name)
	testhelpers.MustGit(t, dir, "commit", "-m", "add "+name)
	return testhelpers.MustGit(t, dir, "rev-parse", "HEAD")
}

// RebaseOntoUpstream replays only upstream..HEAD: commits reachable from the
// upstream (here an orphaned integration commit) are left out.
func TestRebaseOntoUpstreamExcludesUpstreamCommits(t *testing.T) {
	repoDir := setupTestRepo(t)
	g := New(repoDir)
	before := testhelpers.MustGit(t, repoDir, "rev-parse", "integration")
	orphan := commitFile(t, repoDir, "orphan.txt", "orphan\n")
	const taskID = "task-onto-upstream"
	if _, err := g.CreateWorktree(taskID, "integration"); err != nil {
		t.Fatal(err)
	}
	wtPath := g.GetWorktreePath(taskID)
	commitFile(t, wtPath, "task.txt", "task\n")
	testhelpers.MustGit(t, repoDir, "reset", "--hard", before)
	target := commitFile(t, repoDir, "other.txt", "other\n")

	if err := g.RebaseOntoUpstream(wtPath, target, orphan); err != nil {
		t.Fatalf("RebaseOntoUpstream() error = %v", err)
	}
	commits, merges, err := g.RangeCommits(target, "task/"+taskID)
	if err != nil || merges || len(commits) != 1 {
		t.Fatalf("RangeCommits(target, task) = %v, merges %v, %v; want the single task commit", commits, merges, err)
	}
	if _, err := os.Stat(filepath.Join(wtPath, "orphan.txt")); !os.IsNotExist(err) {
		t.Errorf("orphan.txt present after rebase (stat err %v); the upstream commit must be left out", err)
	}
}

func TestRangeCommitsReportsMergesOldestFirst(t *testing.T) {
	repoDir := setupTestRepo(t)
	g := New(repoDir)
	base := testhelpers.MustGit(t, repoDir, "rev-parse", "HEAD")
	first := commitFile(t, repoDir, "first.txt", "1\n")
	testhelpers.MustGit(t, repoDir, "checkout", "-b", "side", base)
	commitFile(t, repoDir, "side.txt", "s\n")
	testhelpers.MustGit(t, repoDir, "checkout", "integration")
	testhelpers.MustGit(t, repoDir, "merge", "--no-ff", "-m", "merge side", "side")

	commits, merges, err := g.RangeCommits(base, "integration")
	if err != nil || !merges || len(commits) != 3 || commits[0] == "" {
		t.Fatalf("RangeCommits() = %v, merges %v, %v; want 3 commits including a merge", commits, merges, err)
	}
	linear, merges, err := g.RangeCommits(base, first)
	if err != nil || merges || len(linear) != 1 || linear[0] != first {
		t.Fatalf("RangeCommits(base, first) = %v, merges %v, %v; want [%s]", linear, merges, err, first)
	}
}

func TestGetInterruptedRebaseDescribesStoppedRebase(t *testing.T) {
	repoDir := setupTestRepo(t)
	g := New(repoDir)
	const taskID = "task-head-name"
	if _, err := g.CreateWorktree(taskID, "integration"); err != nil {
		t.Fatal(err)
	}
	wtPath := g.GetWorktreePath(taskID)
	if rebase, err := g.GetInterruptedRebase(wtPath); err != nil || rebase != nil {
		t.Fatalf("GetInterruptedRebase() at rest = %+v, %v; want nil", rebase, err)
	}
	origHead := commitFile(t, wtPath, "shared.txt", "task\n")
	commitFile(t, repoDir, "shared.txt", "integration\n")
	err := g.RebaseOnto(wtPath, "integration")
	var conflict *RebaseConflictError
	if !stderrors.As(err, &conflict) {
		t.Fatalf("RebaseOnto() error = %v, want a conflict", err)
	}
	want := InterruptedRebase{HeadName: "refs/heads/task/" + taskID, OrigHead: origHead}
	if rebase, err := g.GetInterruptedRebase(wtPath); err != nil || rebase == nil || *rebase != want {
		t.Fatalf("GetInterruptedRebase() = %+v, %v; want %+v", rebase, err, want)
	}
}
