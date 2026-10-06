package cli

import (
	"path/filepath"
	"strconv"
	"testing"
)

func TestRunCLICompletionRuntimeSupportsZshAndFish(t *testing.T) {
	opts := seedCompletionStores(t)
	for _, shell := range []string{"zsh", "fish"} {
		t.Run(shell+" profile names", func(t *testing.T) {
			got := runCompletionRuntime(t, opts, shellCompleteArgs(shell, 3, "podlaz", "profile", "show", "")...)
			assertContainsCandidateValue(t, got, "Alpha")
			assertContainsCandidateValue(t, got, "Bravo")
			assertNotContainsCandidateValue(t, got, "alpha")
		})
		t.Run(shell+" subscription ids", func(t *testing.T) {
			got := runCompletionRuntime(t, opts, shellCompleteArgs(shell, 3, "podlaz", "subscription", "show", "")...)
			assertContainsCandidateValue(t, got, "personal")
			assertContainsCandidateValue(t, got, "work")
		})
		t.Run(shell+" debug doctor flags", func(t *testing.T) {
			got := runCompletionRuntime(t, opts, shellCompleteArgs(shell, 3, "podlaz", "debug", "doctor", "--")...)
			assertContainsCandidateValue(t, got, "--tun")
			assertContainsCandidateValue(t, got, "--json")
		})
		t.Run(shell+" import files", func(t *testing.T) {
			got := runCompletionRuntime(t, opts, shellCompleteArgs(shell, 2, "podlaz", "import", "")...)
			assertContainsLine(t, got, ":default-files")
			assertNotContainsLine(t, got, ":no-files")
		})
	}
}

func TestRunCLICompletionRuntimeZshAndFishMissingStateIsQuiet(t *testing.T) {
	dir := t.TempDir()
	opts := options{
		profileStorePath:      filepath.Join(dir, "missing-profiles.json"),
		subscriptionStorePath: filepath.Join(dir, "missing-subscriptions.json"),
	}
	for _, shell := range []string{"zsh", "fish"} {
		got := runCompletionRuntime(t, opts, shellCompleteArgs(shell, 2, "podlaz", "connect", "")...)
		assertContainsLine(t, got, ":no-files")
		assertNotContainsCandidateValue(t, got, "Alpha")
	}
}

func shellCompleteArgs(shell string, cursor int, words ...string) []string {
	args := []string{shell, strconv.Itoa(cursor)}
	return append(args, words...)
}
