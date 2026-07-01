package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIProfileDeleteInteractiveDefaultYes(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{
		profileStorePath: storePath,
		stdin:            strings.NewReader("\n"),
		stdinIsTerminal:  func() bool { return true },
	}
	addTestProfile(t, opts)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "delete", "test"}, &out, opts); err != nil {
		t.Fatalf("interactive profile delete failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{"Delete profile test? Type yes to continue [Y/n]:", "Profile deleted: test"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got %q", want, got)
		}
	}
}

func TestRunCLIProfileDeleteInteractiveInvalidThenNoPreservesProfile(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{
		profileStorePath: storePath,
		stdin:            strings.NewReader("maybe\nno\n"),
		stdinIsTerminal:  func() bool { return true },
	}
	addTestProfile(t, opts)

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"profile", "delete", "test"}, &out, opts)
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "profile delete canceled") {
		t.Fatalf("expected interactive profile delete cancel with exit code 1, got %v", err)
	}
	if !strings.Contains(out.String(), "Please answer y or n.") {
		t.Fatalf("expected invalid input retry guidance, got %q", out.String())
	}
	if err := runWithOptions(context.Background(), []string{"profile", "show", "test"}, &bytes.Buffer{}, options{profileStorePath: storePath}); err != nil {
		t.Fatalf("profile was not preserved after cancel: %v", err)
	}
}
