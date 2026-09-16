package e2e_test

import (
	"strings"
	"testing"
)

func TestHostedSyntheticTUNConnectFailureDiagnosticIsPrivateAndBounded(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	for _, marker := range []string{
		"diagnose_connect_failure()",
		"/run/podlaz/diagnostics/tun-last.json",
		"primary_classification",
		"failure_phase",
		"connect-diagnostic=%s/%s",
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("temporary hosted connect diagnostic lost %q", marker)
		}
	}
	for _, forbidden := range []string{
		"cat ${GUEST_PRIVATE}/connect.stderr",
		".errors",
		".error",
		".network",
		".session.profile",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("temporary hosted connect diagnostic exposes private evidence via %q", forbidden)
		}
	}
}
