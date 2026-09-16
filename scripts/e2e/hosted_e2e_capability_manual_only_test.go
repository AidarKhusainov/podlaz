package e2e_test

import (
	"strings"
	"testing"
)

func TestHostedE2ECapabilityWorkflowRemainsManualOnly(t *testing.T) {
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)

	requireHostedCapabilityMarkers(t, workflow,
		"workflow_dispatch:",
	)

	for _, automaticTrigger := range []string{"\n  pull_request:", "\n  push:"} {
		if strings.Contains(workflow, automaticTrigger) {
			t.Fatalf("capability spike must remain manual-only; found automatic trigger %q", automaticTrigger)
		}
	}

	if strings.Contains(workflow, "PODLAZ_BUILT: Sep 14 2026") {
		t.Fatal("capability reproducer must not pin a stale build timestamp")
	}
}
