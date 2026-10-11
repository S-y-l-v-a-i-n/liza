package prompts

import (
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/brand"
)

// D-100: blocked-task assessments judge readiness from the engine's
// waiting_on, never from depends_on alone, and treat its heads as diagnosis
// rather than --awaits targets.
func TestBlockedWakeJudgesReadinessFromWaitingOn(t *testing.T) {
	withPromptBrandValues(t, func() {
		brand.NameTitle = "Acme"
		brand.BinaryName = "acme-cli"
		brand.GlobalDirName = ".acme"
		brand.ProjectDirName = ".acme-run"
	})
	output, err := buildInstructionsForWakeTrigger("BLOCKED_TASKS", "orchestrator-1",
		wakeTemplateData{BlockedScope: &blockedScopeData{TaskIDs: []string{"task-1"}}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"`waiting_on` in acme-cli get <id> --json",
		"never from depends_on alone",
		"not claimability",
		"Heads are not await targets",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("blocked wake missing %q", want)
		}
	}
	for _, leaked := range []string{"Liza", "LIZA", "liza", "{{binaryName}}"} {
		if strings.Contains(output, leaked) {
			t.Errorf("nondefault blocked wake leaks %q", leaked)
		}
	}
}
