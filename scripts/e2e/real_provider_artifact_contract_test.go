package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestRealProviderUploadsRequirePrivateExecutionAndFailClosedPublication(t *testing.T) {
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
			"E2E_ARTIFACT_DIR: ${{ runner.temp }}/podlaz-e2e-tmp/real-provider-artifacts",
			"id: data_plane",
			"real-provider-result.txt",
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
