package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPreflightXrayTunSupportUsesConfigTestAndRemovesTrackedRuntimeConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	xray := writeXrayPreflightExecutable(t, filepath.Join(dir, "xray"), `#!/bin/sh
test "$1" = "test"
test "$2" = "-config"
test -f "$3"
case "$3" in
  */xray.json) exit 0 ;;
  *) exit 9 ;;
esac
`)
	runtimeConfigPath := filepath.Join(dir, "generated", generatedXrayName)

	err := preflightXrayTunSupport(context.Background(), xray, runtimeConfigPath, []byte("{}\n"), sameUserCoreExecutionIdentity())
	if err != nil {
		t.Fatalf("preflight Xray TUN support: %v", err)
	}
	if _, statErr := os.Stat(runtimeConfigPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected tracked runtime config to be removed after successful preflight, stat err=%v", statErr)
	}
}

func TestPreflightXrayTunSupportReturnsStableUnsupportedError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test")
	}
	dir := t.TempDir()
	xray := writeXrayPreflightExecutable(t, filepath.Join(dir, "xray"), `#!/bin/sh
echo 'unknown inbound protocol: tun' >&2
exit 23
`)
	runtimeConfigPath := filepath.Join(dir, "generated", generatedXrayName)

	err := preflightXrayTunSupport(context.Background(), xray, runtimeConfigPath, []byte("{}\n"), sameUserCoreExecutionIdentity())
	if !errors.Is(err, errXrayTunUnsupported) {
		t.Fatalf("expected Xray TUN unsupported error, got %v", err)
	}
	if !strings.Contains(err.Error(), "TUN mode requires an Xray-core build with tun inbound support") {
		t.Fatalf("expected stable preflight message, got %v", err)
	}
	if strings.Contains(err.Error(), string([]byte{0})) {
		t.Fatalf("unexpected binary data in error: %v", err)
	}
}

func writeXrayPreflightExecutable(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	return path
}
