package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNMOwnedXrayCompatibilityValidatesExactConfigWithoutFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	script := filepath.Join(dir, "official-xray")
	logPath := filepath.Join(dir, "invocation.txt")
	t.Setenv("XRAY_TEST_RECORD", logPath)
	command := `#!/bin/sh
test "$1" = run && test "$2" = -test && test "$3" = -config || exit 12
test -f "$4" || exit 13
printf 'run -test -config\n' >> "$XRAY_TEST_RECORD"
if grep -q 'unsupported' "$4"; then
  echo 'provider secret in Xray diagnostic' >&2
  exit 23
fi
`
	if err := os.WriteFile(script, []byte(command), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := preflightNMOwnedXrayCompatibilityWithRuntime(context.Background(), script, []byte(`{"settings":"compatible"}`)); err != nil {
		t.Fatalf("valid config preflight: %v", err)
	}
	err := preflightNMOwnedXrayCompatibilityWithRuntime(context.Background(), script, []byte(`{"settings":"unsupported"}`))
	if !errors.Is(err, errNMOwnedXrayIncompatible) || strings.Contains(err.Error(), "provider secret") {
		t.Fatalf("incompatibility must fail closed without exposing Xray diagnostics: %v", err)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(contents), "run -test -config") != 2 {
		t.Fatalf("preflight did not use only explicit Xray config tests: %q", contents)
	}
	files, err := filepath.Glob(filepath.Join(dir, "podlaz-nm-xray-compat-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("private preflight config was not cleaned up: files=%v err=%v", files, err)
	}
}

func TestNMOwnedXrayCompatibilityRefusesMissingConfigAndCancelledContext(t *testing.T) {
	if err := preflightNMOwnedXrayCompatibilityWithRuntime(context.Background(), "/bin/false", nil); !errors.Is(err, errNMOwnedXrayIncompatible) {
		t.Fatalf("missing config must fail closed: %v", err)
	}
	if err := preflightNMOwnedXrayCompatibilityWithRuntime(context.Background(), "xray", []byte("{}")); !errors.Is(err, errNMOwnedXrayIncompatible) {
		t.Fatalf("PATH-based unpinned binary must not be accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := preflightNMOwnedXrayCompatibilityWithRuntime(ctx, "/bin/false", []byte("{}")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preflight must not attempt a runtime launch: %v", err)
	}
}
