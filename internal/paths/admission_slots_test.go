package paths

import (
	"path/filepath"
	"testing"

	"github.com/liza-mas/liza/internal/brand"
)

func TestAdmissionSlotsDirUsesBrand(t *testing.T) {
	previous := brand.ProjectDirName
	brand.ProjectDirName = ".counter-brand"
	t.Cleanup(func() { brand.ProjectDirName = previous })
	root := t.TempDir()
	if got, want := New(root).AdmissionSlotsDir(), filepath.Join(root, ".counter-brand", "admission-slots"); got != want {
		t.Fatalf("admission slots directory=%q, want %q", got, want)
	}
}
