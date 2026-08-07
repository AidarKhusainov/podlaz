package recovery

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanWithOptionsReportsResolvedInspectionUnknownWhenResolvectlUnavailable(t *testing.T) {
	plan := PlanWithOptions(context.Background(), Options{
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
		Runner:     fakeMissingResourcesRunner(),
	})

	assertNoCandidateKind(t, plan, managedDNSCandidateKind)
	if len(plan.Warnings) != 1 {
		t.Fatalf("missing resolvectl must produce exactly one inspection warning, got %#v", plan.Warnings)
	}
	warning := plan.Warnings[0]
	if warning.Target != managedDNSTarget || !strings.Contains(warning.Message, "resolvectl command is unavailable") {
		t.Fatalf("unexpected resolved inspection warning: %#v", warning)
	}
	if strings.Contains(plan.String(), "No podlaz-owned recovery candidates found.") {
		t.Fatalf("unknown resolved state must not be published as a clean host: %q", plan.String())
	}
}
