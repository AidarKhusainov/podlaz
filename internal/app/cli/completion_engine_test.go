package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
)

func TestCompletionSubscriptionDeleteCompletesCommandIDAndFlags(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	if err := runWithOptions(context.Background(), []string{"subscription", "add", "--name", "personal", "--url", localFileURL(filepath.Join(dir, "sub.txt"))}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription add failed: %v", err)
	}

	commands := completeTunWarden(completionRequest{Shell: "bash", Cursor: 2, Words: []string{"tunwarden", "subscription", ""}}, opts)
	assertCompletionCandidate(t, commands, "delete")

	ids := completeTunWarden(completionRequest{Shell: "zsh", Cursor: 3, Words: []string{"tunwarden", "subscription", "delete", ""}}, opts)
	assertCompletionCandidateDescription(t, ids, "personal", "personal")

	flags := completeTunWarden(completionRequest{Shell: "fish", Cursor: 4, Words: []string{"tunwarden", "subscription", "delete", "personal", "--"}}, opts)
	assertCompletionCandidate(t, flags, "--yes")
	assertCompletionCandidate(t, flags, "--keep-profiles")
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

	ids := completeTunWarden(completionRequest{Shell: "bash", Cursor: 2, Words: []string{"tunwarden", "connect", ""}}, opts)
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
