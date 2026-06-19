package cli

import (
	"path/filepath"
	"strconv"
	"testing"
)

func TestRunCLICompletionRuntimeSupportsZshAndFish(t *testing.T) {
	opts := seedCompletionStores(t)
	for _, shell := range []string{"zsh", "fish"} {
		t.Run(shell+" profile ids", func(t *testing.T) {
			got := runCompletionRuntime(t, opts, shellCompleteArgs(shell, 3, "podlaz", "profile", "show", "")...)
			assertContainsCandidateLine(t, got, "alpha", "Alpha")
			assertContainsCandidateLine(t, got, "bravo", "Bravo")
			assertNotContainsCandidateValue(t, got, "personal")
		})
		t.Run(shell+" subscription ids", func(t *testing.T) {
			got := runCompletionRuntime(t, opts, shellCompleteArgs(shell, 3, "podlaz", "subscription", "show", "")...)
			assertContainsCandidateLine(t, got, "personal", "Personal")
			assertContainsCandidateLine(t, got, "work", "Work")
			assertNotContainsCandidateValue(t, got, "alpha")
		})
		t.Run(shell+" mode values", func(t *testing.T) {
			got := runCompletionRuntime(t, opts, shellCompleteArgs(shell, 3, "podlaz", "plan", "--mode", "")...)
			assertContainsLine(t, got, "proxy-only")
			assertContainsLine(t, got, "tun")
		})
		t.Run(shell+" protocol values", func(t *testing.T) {
			got := runCompletionRuntime(t, opts, shellCompleteArgs(shell, 4, "podlaz", "profile", "add", "--protocol", "")...)
			assertContainsLine(t, got, "vless")
			assertContainsLine(t, got, "vmess")
			assertContainsLine(t, got, "trojan")
			assertContainsLine(t, got, "shadowsocks")
		})
		t.Run(shell+" filters used flags", func(t *testing.T) {
			got := runCompletionRuntime(t, opts, shellCompleteArgs(shell, 4, "podlaz", "plan", "--mode", "tun", "-")...)
			assertContainsLine(t, got, "--json")
			assertNotContainsLine(t, got, "--mode")
		})
		t.Run(shell+" import keeps file completion", func(t *testing.T) {
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
		t.Run(shell, func(t *testing.T) {
			got := runCompletionRuntime(t, opts, shellCompleteArgs(shell, 2, "podlaz", "connect", "")...)
			assertContainsLine(t, got, ":no-files")
			assertNotContainsCandidateValue(t, got, "alpha")
		})
	}
}

func shellCompleteArgs(shell string, cursor int, words ...string) []string {
	args := []string{shell, strconv.Itoa(cursor)}
	return append(args, words...)
}
