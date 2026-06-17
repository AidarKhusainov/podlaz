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
	assertCompletionCandidate(t, ids, "personal")

	flags := completeTunWarden(completionRequest{Shell: "fish", Cursor: 4, Words: []string{"tunwarden", "subscription", "delete", "personal", "--"}}, opts)
	assertCompletionCandidate(t, flags, "--yes")
	assertCompletionCandidate(t, flags, "--keep-profiles")
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
