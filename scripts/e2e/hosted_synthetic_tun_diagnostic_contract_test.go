package e2e_test

import (
	"strings"
	"testing"
)

func TestHostedSyntheticTUNConnectFailureDiagnosticIsPrivateAndBounded(t *testing.T) {
	workflow := readHostedSyntheticTUNFile(t, hostedSyntheticTUNWorkflow)
	start := strings.Index(workflow, "- name: Diagnose private connect failure")
	end := strings.Index(workflow, "- name: Validate public report")
	if start < 0 || end <= start {
		t.Fatal("temporary hosted connect diagnostic step boundaries not found")
	}
	block := workflow[start:end]
	for _, marker := range []string{
		"outer-tcp53=pass",
		"outer-tcp53=fail",
		"outer-udp53=pass",
		"outer-udp53=fail",
		"socket.SOCK_DGRAM",
	} {
		if !strings.Contains(block, marker) {
			t.Fatalf("temporary hosted connect diagnostic lost %q", marker)
		}
	}
	for _, forbidden := range []string{
		"cat ${CONNECT_STDERR}",
		"actions/upload-artifact",
	} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("temporary hosted connect diagnostic exposes private evidence via %q", forbidden)
		}
	}
}
