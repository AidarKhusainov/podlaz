package daemon

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveXrayRuntimeClassifiesMissingHelper(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "xray")

	_, err := resolveRuntimeExecutable(missing, "PODLAZ_XRAY_PATH", "xray", "Xray")
	if err == nil {
		t.Fatal("expected missing helper error")
	}
	if !isRuntimeUnavailableError(err) {
		t.Fatalf("expected runtime unavailable error, got %T: %v", err, err)
	}
	for _, want := range []string{
		"TUN mode cannot start because Xray is unavailable.",
		"Expected: " + missing,
		"No network changes were applied.",
		"Run: plz doctor",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got:\n%s", want, err)
		}
	}
}

func TestResolveXrayRuntimeRejectsNonExecutableHelper(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xray")
	if err := os.WriteFile(path, []byte("not executable\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := resolveRuntimeExecutable(path, "PODLAZ_XRAY_PATH", "xray", "Xray")
	if err == nil {
		t.Fatal("expected non-executable helper error")
	}
	if !isRuntimeUnavailableError(err) {
		t.Fatalf("expected runtime unavailable error, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "file is not executable") {
		t.Fatalf("expected executable-bit guidance, got:\n%s", err)
	}
}

func TestDaemonAPIHTTPStatusCodeMapsRuntimeUnavailableToServiceUnavailable(t *testing.T) {
	err := newRuntimeUnavailableError("nftables", "Command \"nft\" was not found in PATH.")
	if got := daemonAPIHTTPStatusCode(err); got != http.StatusServiceUnavailable {
		t.Fatalf("unexpected HTTP status: got %d want %d", got, http.StatusServiceUnavailable)
	}
}
