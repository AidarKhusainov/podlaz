package tundiag

import (
	"strings"
	"testing"
)

func TestRenderHumanVerboseShowsProbeFailurePhase(t *testing.T) {
	report := Report{Probes: []ProbeResult{{
		ID:             "tcp-443",
		Layer:          LayerTCP,
		Status:         ProbeFail,
		Classification: ClassTimeout,
		FailurePhase:   FailurePhaseRouteLookup,
		Error:          "route lookup timed out",
	}}}

	verbose := RenderHuman(report, true)
	if !strings.Contains(verbose, "failure phase: route_lookup") {
		t.Fatalf("verbose output omitted probe failure phase: %s", verbose)
	}
	compact := RenderHuman(report, false)
	if strings.Contains(compact, "failure phase:") {
		t.Fatalf("compact output unexpectedly included verbose phase evidence: %s", compact)
	}
}
