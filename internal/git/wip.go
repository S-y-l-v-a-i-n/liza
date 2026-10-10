package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// interruptedOperationMarkers are the per-worktree git-dir entries Git leaves
// while an operation waits for the user to resolve or continue it.
var interruptedOperationMarkers = []struct {
	path string
	name string
}{
	{"MERGE_HEAD", "merge"},
	{"rebase-merge", "rebase"},
	{"rebase-apply", "rebase"},
	{"CHERRY_PICK_HEAD", "cherry-pick"},
	{"REVERT_HEAD", "revert"},
}

// InterruptedOperation names the Git operation left in progress in a worktree
// (merge, rebase, cherry-pick or revert), or "conflict resolution" when the
// index holds unmerged entries.
// It returns "" when the worktree is at rest. Staging such a worktree would
// mark conflicts as resolved, and committing it could complete the operation.
func (g *Git) InterruptedOperation(wtPath string) (string, error) {
	for _, marker := range interruptedOperationMarkers {
		markerPath, err := g.execInDir(wtPath, "rev-parse", "--git-path", marker.path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(markerPath) {
			markerPath = filepath.Join(wtPath, markerPath)
		}
		if _, err := os.Stat(markerPath); err == nil {
			return marker.name, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect %s: %w", marker.path, err)
		}
	}
	unmerged, err := g.execInDir(wtPath, "ls-files", "--unmerged")
	if err != nil {
		return "", err
	}
	if unmerged != "" {
		return "conflict resolution", nil
	}
	return "", nil
}

// InterruptedRebase describes a rebase left in progress in a worktree.
type InterruptedRebase struct {
	HeadName string // branch ref being rewritten
	OrigHead string // HEAD when the rebase started
}

// GetInterruptedRebase returns the rebase in progress in wtPath, or nil when
// none is. Both rebase backends record head-name and orig-head.
func (g *Git) GetInterruptedRebase(wtPath string) (*InterruptedRebase, error) {
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		stateDir, err := g.execInDir(wtPath, "rev-parse", "--git-path", dir)
		if err != nil {
			return nil, err
		}
		if !filepath.IsAbs(stateDir) {
			stateDir = filepath.Join(wtPath, stateDir)
		}
		if _, err := os.Stat(filepath.Join(stateDir, "head-name")); os.IsNotExist(err) {
			continue
		}
		var fields [2]string
		for i, name := range []string{"head-name", "orig-head"} {
			data, err := os.ReadFile(filepath.Join(stateDir, name))
			if err != nil {
				return nil, fmt.Errorf("inspect %s/%s: %w", dir, name, err)
			}
			fields[i] = strings.TrimSpace(string(data))
		}
		return &InterruptedRebase{HeadName: fields[0], OrigHead: fields[1]}, nil
	}
	return nil, nil
}

// CommitAllWIP stages every change in a worktree, tracked and untracked, and
// commits it without running hooks. The commit records interrupted work so it
// survives a claim release; it attests no quality. committed is false, and no
// commit is made, when there is nothing to commit. If the commit itself fails
// the changes stay staged, with their content untouched.
func (g *Git) CommitAllWIP(wtPath, message string) (sha string, committed bool, err error) {
	if _, err := g.execInDir(wtPath, "add", "-A"); err != nil {
		return "", false, err
	}
	staged, err := g.execInDir(wtPath, "diff", "--cached", "--name-only")
	if err != nil {
		return "", false, err
	}
	if staged == "" {
		return "", false, nil
	}
	if _, err := g.execInDir(wtPath, "commit", "--no-verify", "-m", message); err != nil {
		return "", false, err
	}
	sha, err = g.execInDir(wtPath, "rev-parse", "HEAD")
	if err != nil {
		return "", true, err
	}
	return sha, true, nil
}
