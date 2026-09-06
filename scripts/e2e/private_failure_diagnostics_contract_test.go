package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestReleaseRealProviderScansPublicDiagnosticsBeforePrivateCleanup(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(data)

	scan := strings.Index(workflow, "Scan public artifacts")
	cleanup := strings.Index(workflow, "Remove private E2E temp state")
	if scan < 0 || cleanup < 0 || scan >= cleanup {
		t.Fatal("real-provider public artifact scan must run before private E2E temp cleanup")
	}

	scanner, err := os.ReadFile("scan-public-artifacts.sh")
	if err != nil {
		t.Fatalf("read public artifact scanner: %v", err)
	}
	for _, required := range []string{
		"lib/tun_soak_metrics.py",
		"classify-cli-error",
	} {
		if !strings.Contains(string(scanner), required) {
			t.Fatalf("public artifact scanner must reuse private CLI failure classification %q", required)
		}
	}
}
