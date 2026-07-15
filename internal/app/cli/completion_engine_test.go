package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
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
	profileID := storeCompletionProfile(t, opts, "russia-1", "Russia 1")

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

func TestCompletionConnectCompletesHandoffPolicyValues(t *testing.T) {
	flags := completepodlaz(completionRequest{Shell: "bash", Cursor: 2, Words: []string{"podlaz", "connect", "--"}}, options{})
	assertCompletionCandidate(t, flags, "--handoff")

	values := completepodlaz(completionRequest{Shell: "bash", Cursor: 3, Words: []string{"podlaz", "connect", "--handoff", ""}}, options{})
	assertCompletionCandidate(t, values, "block")

	inlineValues := completepodlaz(completionRequest{Shell: "zsh", Cursor: 2, Words: []string{"podlaz", "connect", "--handoff="}}, options{})
	assertCompletionCandidate(t, inlineValues, "--handoff=block")
}

func TestCompletionDoctorCompletesTunFormats(t *testing.T) {
	flags := completepodlaz(completionRequest{Shell: "bash", Cursor: 2, Words: []string{"podlaz", "doctor", "--"}}, options{})
	for _, want := range []string{"--tun", "--verbose", "-v", "--json"} {
		assertCompletionCandidate(t, flags, want)
	}
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
	profileID := storeCompletionProfile(t, opts, "russia-1", "Russia 1")

	ids := completepodlaz(completionRequest{Shell: "bash", Cursor: 2, Words: []string{"podlaz", "connect", ""}}, opts)
	assertCompletionCandidateDescription(t, ids, profileID, "Russia 1")
}

func storeCompletionProfile(t *testing.T, opts options, id string, name string) string {
	t.Helper()
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		t.Fatal(err)
	}
	p := testConnectProfile()
	p.ID = id
	p.Name = name
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}
	return p.ID
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
