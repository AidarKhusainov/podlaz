package recovery

import (
	"context"
	"strings"
	"testing"
)

func TestPlanStringExplainsDryRunInspectionSource(t *testing.T) {
	plan := PlanWithOptions(context.Background(), Options{Scanner: fakeScanner{}})
	got := plan.String()
	for _, want := range []string{
		"Inspection: read-only",
		"uses daemon startup scan when available plus local safe checks",
		"Local permission warnings can differ from daemon-owned --execute cleanup",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected recovery dry-run output to contain %q, got %q", want, got)
		}
	}
}
