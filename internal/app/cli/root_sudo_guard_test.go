package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLISudoGuardRejectsUserStateCommandsBeforeStoreAccess(t *testing.T) {
	withSudoRootInvocation(t)

	stateDir := t.TempDir()
	profileStorePath := filepath.Join(stateDir, "root", "profiles.json")
	subscriptionStorePath := filepath.Join(stateDir, "root", "subscriptions.json")
	opts := options{profileStorePath: profileStorePath, subscriptionStorePath: subscriptionStorePath}

	for _, tt := range []struct {
		name      string
		args      []string
		wantShape string
	}{
		{
			name:      "import redacts target",
			args:      []string{"import", "https://provider.example/sub/opaquevalue"},
			wantShape: "podlaz import <target>",
		},
		{
			name:      "profile list",
			args:      []string{"profile", "list"},
			wantShape: "podlaz profile list",
		},
		{
			name:      "subscription show redacts id",
			args:      []string{"subscription", "show", "sub-opaquevalue"},
			wantShape: "podlaz subscription show <subscription-id>",
		},
		{
			name:      "plan redacts profile id",
			args:      []string{"plan", "--mode", "tun", "profile-opaquevalue"},
			wantShape: "podlaz plan --mode <mode> <profile-id>",
		},
		{
			name:      "connect redacts profile id",
			args:      []string{"connect", "--mode", "proxy-only", "profile-opaquevalue"},
			wantShape: "podlaz connect [--mode proxy-only|tun] <profile-id>",
		},
		{
			name:      "check redacts profile id",
			args:      []string{"check", "profile-opaquevalue"},
			wantShape: "podlaz check <profile-id>",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := runWithOptions(context.Background(), tt.args, &out, opts)
			assertSudoUserStateError(t, err, out.String(), tt.wantShape)
			assertDoesNotContain(t, err.Error(), "opaquevalue")
			assertPathDoesNotExist(t, profileStorePath)
			assertPathDoesNotExist(t, subscriptionStorePath)
		})
	}
}

func TestRunCLISudoGuardKeepsStaticCommandsUsable(t *testing.T) {
	withSudoRootInvocation(t)

	for _, tt := range []struct {
		name       string
		args       []string
		wantOutput string
	}{
		{name: "version", args: []string{"version"}, wantOutput: "podlaz"},
		{name: "help topic", args: []string{"help", "profile"}, wantOutput: "Usage:\n  podlaz profile"},
		{name: "guarded command help", args: []string{"profile", "--help"}, wantOutput: "Usage:\n  podlaz profile"},
		{name: "completion generation", args: []string{"completion", "bash"}, wantOutput: "bash completion for podlaz"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := runWithOptions(context.Background(), tt.args, &out, options{}); err != nil {
				t.Fatalf("expected static command to work under sudo-like invocation, got %v", err)
			}
			if got := out.String(); !strings.Contains(got, tt.wantOutput) {
				t.Fatalf("expected output to contain %q, got %q", tt.wantOutput, got)
			}
		})
	}
}

func TestRunCLISudoGuardRejectsDynamicCompletionBeforeStoreAccess(t *testing.T) {
	withSudoRootInvocation(t)

	stateDir := t.TempDir()
	profileStorePath := filepath.Join(stateDir, "root", "profiles.json")
	subscriptionStorePath := filepath.Join(stateDir, "root", "subscriptions.json")
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"__complete", "bash", "3", "podlaz", "profile", "show", "profile-opaquevalue"}, &out, options{
		profileStorePath:      profileStorePath,
		subscriptionStorePath: subscriptionStorePath,
	})

	assertSudoUserStateError(t, err, out.String(), "podlaz profile show <profile-id>")
	assertDoesNotContain(t, err.Error(), "profile-opaquevalue")
	assertPathDoesNotExist(t, profileStorePath)
	assertPathDoesNotExist(t, subscriptionStorePath)
}

func TestRunCLISudoGuardAllowsStaticCompletionRuntime(t *testing.T) {
	withSudoRootInvocation(t)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"__complete", "bash", "1", "podlaz", ""}, &out, options{}); err != nil {
		t.Fatalf("expected static completion runtime to work under sudo-like invocation, got %v", err)
	}
	if got := out.String(); !strings.Contains(got, "profile") || !strings.Contains(got, ":no-files") {
		t.Fatalf("expected static completion candidates, got %q", got)
	}
}

func withSudoRootInvocation(t *testing.T) {
	t.Helper()
	oldEffectiveUID := currentEffectiveUID
	oldSudoUser := currentSudoUser
	currentEffectiveUID = func() int { return 0 }
	currentSudoUser = func() string { return "aidar" }
	t.Cleanup(func() {
		currentEffectiveUID = oldEffectiveUID
		currentSudoUser = oldSudoUser
	})
}

func assertSudoUserStateError(t *testing.T, err error, stdout string, wantShape string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected sudo user-state guard error")
	}
	if got := ExitCode(err); got != 4 {
		t.Fatalf("expected exit code 4, got %d", got)
	}
	for _, want := range []string{
		"this command uses user-owned state and must not be run with sudo",
		"Run it as your user:",
		wantShape,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error containing %q, got %q", want, err.Error())
		}
	}
	if stdout != "" {
		t.Fatalf("expected no stdout on sudo user-state guard error, got %q", stdout)
	}
}

func assertDoesNotContain(t *testing.T, got string, forbidden string) {
	t.Helper()
	if strings.Contains(got, forbidden) {
		t.Fatalf("expected %q not to contain %q", got, forbidden)
	}
}

func assertPathDoesNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s not to exist, stat error: %v", path, err)
	}
}
