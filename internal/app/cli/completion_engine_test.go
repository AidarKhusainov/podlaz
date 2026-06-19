package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletionSubscriptionDeleteCompletesCommandIDAndFlags(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	if err := runWithOptions(context.Background(), []string{"subscription", "add", "--name", "personal", "--url", localFileURL(filepath.Join(dir, "sub.txt"))}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription add failed: %v", err)
	}

	commands := completepodlaz(completionRequest{Shell: "bash", Cursor: 2, Words: []string{"podlaz", "subscription", ""}}, opts)
	assertCompletionCandidate(t, commands, "delete")

	ids := completepodlaz(completionRequest{Shell: "zsh", Cursor: 3, Words: []string{"podlaz", "subscription", "delete", ""}}, opts)
	assertCompletionCandidateDescription(t, ids, "personal", "personal")

	flags := completepodlaz(completionRequest{Shell: "fish", Cursor: 4, Words: []string{"podlaz", "subscription", "delete", "personal", "--"}}, opts)
	assertCompletionCandidate(t, flags, "--yes")
	assertCompletionCandidate(t, flags, "--keep-profiles")
}

func TestCompletionProfileValidateCompletesProfileIDsFlagsAndModeValues(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	uri := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=tls#Russia%201"
	var importOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "import", uri}, &importOut, opts); err != nil {
		t.Fatalf("profile import failed: %v", err)
	}
	profileID := importedProfileIDFromOutput(t, importOut.String())

	commands := completepodlaz(completionRequest{Shell: "bash", Cursor: 2, Words: []string{"podlaz", "profile", ""}}, opts)
	assertCompletionCandidate(t, commands, "validate")

	ids := completepodlaz(completionRequest{Shell: "zsh", Cursor: 3, Words: []string{"podlaz", "profile", "validate", ""}}, opts)
	assertCompletionCandidateDescription(t, ids, profileID, "Russia 1")

	flags := completepodlaz(completionRequest{Shell: "fish", Cursor: 4, Words: []string{"podlaz", "profile", "validate", profileID, "--"}}, opts)
	assertCompletionCandidate(t, flags, "--mode")
	assertCompletionCandidate(t, flags, "--json")

	modeValues := completepodlaz(completionRequest{Shell: "bash", Cursor: 5, Words: []string{"podlaz", "profile", "validate", profileID, "--mode", ""}}, opts)
	assertCompletionCandidate(t, modeValues, "proxy-only")
	assertCompletionCandidate(t, modeValues, "tun")

	inlineModeValues := completepodlaz(completionRequest{Shell: "zsh", Cursor: 4, Words: []string{"podlaz", "profile", "validate", profileID, "--mode="}}, opts)
	assertCompletionCandidate(t, inlineModeValues, "--mode=proxy-only")
	assertCompletionCandidate(t, inlineModeValues, "--mode=tun")
}

func TestCompletionFishScriptIncludesProfileValidateStaticFlags(t *testing.T) {
	var out bytes.Buffer
	printFishCompletion(&out)
	got := out.String()
	for _, want := range []string{
		"__fish_podlaz_using_subcommand profile validate' -l mode -x -a 'proxy-only tun'",
		"__fish_podlaz_using_subcommand profile validate' -l json",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected fish completion script to contain %q, got %q", want, got)
		}
	}
}

func TestCompletionProfileIDsUseDisplayNamesAsDescriptions(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	uri := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=tls#Russia%201"
	var importOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "import", uri}, &importOut, opts); err != nil {
		t.Fatalf("profile import failed: %v", err)
	}
	profileID := importedProfileIDFromOutput(t, importOut.String())

	ids := completepodlaz(completionRequest{Shell: "bash", Cursor: 2, Words: []string{"podlaz", "connect", ""}}, opts)
	assertCompletionCandidateDescription(t, ids, profileID, "Russia 1")
}

func assertCompletionCandidate(t *testing.T, result completionResult, want string) {
	t.Helper()
	for _, candidate := range result.Candidates {
		if candidate.Value == want {
			return
		}
	}
	t.Fatalf("expected completion candidate %q, got %#v", want, result.Candidates)
}

func assertCompletionCandidateDescription(t *testing.T, result completionResult, wantValue string, wantDescription string) {
	t.Helper()
	for _, candidate := range result.Candidates {
		if candidate.Value == wantValue {
			if candidate.Description != wantDescription {
				t.Fatalf("expected completion candidate %q description %q, got %#v", wantValue, wantDescription, candidate)
			}
			return
		}
	}
	t.Fatalf("expected completion candidate %q with description %q, got %#v", wantValue, wantDescription, result.Candidates)
}
