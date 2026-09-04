package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestRealProviderArtifactsStayPrivateUntilRedactionPasses(t *testing.T) {
	dataPlane, err := os.ReadFile("data-plane.sh")
	if err != nil {
		t.Fatalf("read data-plane harness: %v", err)
	}
	script := string(dataPlane)
	for _, required := range []string{
		`PRIVATE_EGRESS_DIR="${E2E_TMP_ROOT}/data-plane-egress"`,
		`PRIVATE_SENSITIVE_VALUES="${E2E_TMP_ROOT}/data-plane-sensitive-values.txt"`,
		`register_private_sensitive_value "${ip4}"`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("data-plane private artifact boundary lost %q", required)
		}
	}
	if strings.Contains(script, `"${E2E_ARTIFACT_DIR}/data-plane-${phase}-${proxy_kind}/public-ipv4.txt"`) {
		t.Fatal("observed real-provider egress IP must not be written to the public artifact directory")
	}
}

func TestRealProviderUploadsRequireSuccessfulRedactionAndPrivateCleanup(t *testing.T) {
	for _, workflow := range []string{
		"../../.github/workflows/integration.yml",
		"../../.github/workflows/release.yml",
	} {
		data, err := os.ReadFile(workflow)
		if err != nil {
			t.Fatalf("read %s: %v", workflow, err)
		}
		text := string(data)
		for _, required := range []string{
			"bash scripts/e2e/scan-public-artifacts.sh",
			"id: evidence_scan",
			"id: private_cleanup",
			"steps.evidence_scan.outcome == 'success'",
			"steps.private_cleanup.outcome == 'success'",
		} {
			if !strings.Contains(text, required) {
				t.Fatalf("%s real-provider artifact gate lost %q", workflow, required)
			}
		}
	}
}
