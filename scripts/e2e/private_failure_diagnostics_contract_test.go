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
	if !strings.Contains(string(scanner), "sanitize-private-failure.sh") {
		t.Fatal("public artifact scanner must reduce private failure evidence before private cleanup")
	}
}
