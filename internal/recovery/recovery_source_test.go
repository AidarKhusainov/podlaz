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
		"uses the authoritative daemon scan when available",
		"local safe checks only as a fallback",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected recovery dry-run output to contain %q, got %q", want, got)
		}
	}
}
