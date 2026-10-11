//go:build !windows

package ops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
)

// A command's background child must not outlive the step that started it:
// releasing the runner lock at the end unlocks the description the child
// shares, so a surviving child would run beside a successor.
func TestDeferredValidationBackgroundChildDoesNotOutliveItsStep(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	spawner := dvCommand(fmt.Sprintf("sleep 60 >/dev/null 2>&1 & echo $! > %q", pidFile), 60)
	f.merge("task-1", map[string]string{"a.txt": "a\n"}, spawner)

	batch := onlyBatch(t, f.run(false))
	if batch.Status != models.DeferredValidationBatchPassed {
		t.Fatalf("batch = %#v", batch)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	// The child may linger briefly as a zombie until init reaps it.
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		select {
		case <-deadline:
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("background child %d survived its batch step", pid)
		case <-tick.C:
		}
	}
}
