package git

import (
	"fmt"
	"strings"

	"github.com/liza-mas/liza/internal/gitenv"
)

// RebaseConflictError indicates a merge conflict during git rebase.
type RebaseConflictError struct {
	Output string // raw git output containing conflict details
}

func (e *RebaseConflictError) Error() string {
	return fmt.Sprintf("rebase conflict: %s", e.Output)
}

// RebaseError indicates a non-conflict git rebase failure.
type RebaseError struct {
	Command []string
	Output  string // combined stdout/stderr from git
	Err     error
}

func (e *RebaseError) Error() string {
	return fmt.Sprintf("rebase failed: %v\nOutput: %s", e.Err, e.Output)
}

func (e *RebaseError) Unwrap() error {
	return e.Err
}

// RebaseOptions configures a worktree rebase.
type RebaseOptions struct {
	Autostash bool
}

// RebaseOnto rebases the current branch in a worktree onto the specified base branch.
// Must be called from within a worktree context.
// Returns *RebaseConflictError for merge conflicts, generic error for other failures.
func (g *Git) RebaseOnto(wtPath string, baseBranch string) error {
	return g.RebaseOntoWithOptions(wtPath, baseBranch, RebaseOptions{})
}

// RebaseOntoWithOptions rebases the current branch in a worktree onto the specified base.
func (g *Git) RebaseOntoWithOptions(wtPath string, baseBranch string, opts RebaseOptions) error {
	args := []string{"rebase"}
	if opts.Autostash {
		args = append(args, "--autostash")
	}
	args = append(args, baseBranch)
	return runRebase(wtPath, args)
}

// RebaseOntoUpstream replays only upstream..HEAD onto newBase
// (`git rebase --onto newBase upstream`), leaving out every commit reachable
// from upstream. Errors are classified as for RebaseOnto.
func (g *Git) RebaseOntoUpstream(wtPath, newBase, upstream string) error {
	return runRebase(wtPath, []string{"rebase", "--onto", newBase, upstream})
}

func runRebase(wtPath string, args []string) error {
	rawOutput, err := gitenv.CombinedOutput(wtPath, args...)
	if err != nil {
		out := string(rawOutput)
		// Classify using canonical git conflict markers from command output only,
		// not from the exec error wrapper, to avoid false positives.
		if strings.Contains(out, "CONFLICT") ||
			strings.Contains(out, "could not apply") {
			return &RebaseConflictError{Output: out}
		}
		return &RebaseError{
			Command: append([]string{"git"}, args...),
			Output:  out,
			Err:     err,
		}
	}
	return nil
}

// AbortRebase aborts an in-progress rebase in a worktree
func (g *Git) AbortRebase(wtPath string) error {
	_, err := g.execInDir(wtPath, "rebase", "--abort")
	if err != nil {
		return fmt.Errorf("failed to abort rebase: %w", err)
	}
	return nil
}
