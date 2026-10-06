package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIProfileDeleteInteractiveEmptyDefaultsNo(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{
		profileStorePath: storePath,
		stdin:            strings.NewReader("\n"),
		stdinIsTerminal:  func() bool { return true },
	}
	addTestProfile(t, opts, "test", "Test")

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"profile", "delete", "Test"}, &out, opts)
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "profile delete canceled") {
		t.Fatalf("empty confirmation err=%v exit=%d", err, ExitCode(err))
	}
	if !strings.Contains(out.String(), "Delete profile Test? [y/N]:") {
		t.Fatalf("default-no prompt missing: %q", out.String())
	}
	if err := runWithOptions(context.Background(), []string{"profile", "show", "Test"}, &bytes.Buffer{}, options{profileStorePath: storePath}); err != nil {
		t.Fatalf("empty confirmation deleted profile: %v", err)
	}
}

func TestRunCLIProfileDeleteInteractiveExplicitYesDeletes(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{
		profileStorePath: storePath,
		stdin:            strings.NewReader("yes\n"),
		stdinIsTerminal:  func() bool { return true },
	}
	addTestProfile(t, opts, "test", "Test")

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "delete", "test"}, &out, opts); err != nil {
		t.Fatalf("explicit yes delete failed: %v", err)
	}
	if !strings.Contains(out.String(), "Profile deleted: Test") {
		t.Fatalf("delete output = %q", out.String())
	}
}

func TestRunCLIProfileDeleteInteractiveEOFCancels(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{
		profileStorePath: storePath,
		stdin:            strings.NewReader(""),
		stdinIsTerminal:  func() bool { return true },
	}
	addTestProfile(t, opts, "test", "Test")

	err := runWithOptions(context.Background(), []string{"profile", "delete", "Test"}, &bytes.Buffer{}, opts)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("EOF err=%v exit=%d, want cancel", err, ExitCode(err))
	}
}
