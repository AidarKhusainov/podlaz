package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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

func TestNativeTunPreflightUnsupportedClassifiesRuntimeUnavailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	xray := writeXrayPreflightExecutable(t, filepath.Join(dir, "xray"), `#!/bin/sh
echo 'unknown inbound protocol: tun' >&2
exit 23
`)

	err := preflightXrayNativeTunSupport(context.Background(), xray, sameUserCoreExecutionIdentity())
	if err == nil {
		t.Fatal("expected unsupported native TUN preflight error")
	}
	if !isRuntimeUnavailableError(err) {
		t.Fatalf("expected runtime unavailable classification, got %T: %v", err, err)
	}
	if !errors.Is(err, errXrayTunUnsupported) {
		t.Fatalf("expected unsupported TUN sentinel in error chain, got %v", err)
	}
	if got := daemonAPIHTTPStatusCode(err); got != http.StatusServiceUnavailable {
		t.Fatalf("unexpected HTTP status: got %d want %d", got, http.StatusServiceUnavailable)
	}
	for _, want := range []string{
		"TUN mode cannot start because Xray TUN support is unavailable.",
		"TUN mode requires an Xray-core build with tun inbound support",
		"No network changes were applied.",
		"Run: plz doctor",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got:\n%s", want, err)
		}
	}
}
