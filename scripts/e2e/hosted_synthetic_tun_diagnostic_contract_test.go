package e2e_test

import (
	"strings"
	"testing"
)

func TestHostedSyntheticTUNConnectFailureDiagnosticIsPrivateAndBounded(t *testing.T) {
	workflow := readHostedSyntheticTUNFile(t, hostedSyntheticTUNWorkflow)
	for _, marker := range []string{
		"Diagnose private connect failure",
		"if: failure()",
		"connect.stderr",
		"classify-cli-error --stderr-file",
		"outer-tcp53=pass",
		"outer-tcp53=fail",
	} {
		if !strings.Contains(workflow, marker) {
			t.Fatalf("temporary hosted connect diagnostic lost %q", marker)
		}
	}
	for _, forbidden := range []string{
		"cat ${CONNECT_STDERR}",
		"upload private connect",
		"actions/upload-artifact",
	} {
		if strings.Contains(workflow[strings.Index(workflow, "Diagnose private connect failure"):], forbidden) {
			t.Fatalf("temporary hosted connect diagnostic exposes private evidence via %q", forbidden)
		}
	}
}
