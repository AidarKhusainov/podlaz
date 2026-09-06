package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestReleaseRealProviderFailurePublishesSanitizedDiagnosticsBeforePrivateCleanup(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(data)

	diagnostic := strings.Index(workflow, "Prepare sanitized failure diagnostics")
	cleanup := strings.Index(workflow, "Remove private E2E temp state")
	if diagnostic < 0 {
		t.Fatal("release real-provider flow must prepare sanitized failure diagnostics")
	}
	if cleanup < 0 || diagnostic >= cleanup {
		t.Fatal("sanitized failure diagnostics must be prepared before private E2E temp cleanup")
	}
	for _, required := range []string{
		"scripts/e2e/sanitize-private-failure.sh",
		"steps.data_plane.outcome",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("release failure diagnostics lost %q", required)
		}
	}
}
