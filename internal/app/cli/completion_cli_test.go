package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/profile"
	"github.com/AidarKhusainov/tunwarden/internal/sub"
)

func TestRunCLICompletionGeneratesSupportedShells(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "bash",
			args: []string{"completion", "bash"},
			want: []string{"_tunwarden()", "__complete bash", "complete -o default -F _tunwarden tunwarden", "proxy-only tun", "vless vmess trojan shadowsocks"},
		},
		{
			name: "zsh",
			args: []string{"completion", "zsh"},
			want: []string{"#compdef tunwarden", "__complete zsh", "_tunwarden \"$@\"", "proxy-only tun", "vless vmess trojan shadowsocks"},
		},
		{
			name: "fish",
			args: []string{"completion", "fish"},
			want: []string{"complete -c tunwarden -f -a", "__complete fish", "proxy-only tun", "vless vmess trojan shadowsocks"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := run(context.Background(), tt.args, &out); err != nil {
				t.Fatalf("completion command failed: %v", err)
			}
			got := out.String()
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("expected completion output to contain %q, got %q", want, got)
				}
			}
		})
	}
}

func TestRunCLICompletionRejectsUnsupportedShell(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"completion", "powershell"}, &out)
	assertUsageError(t, err, out.String(), "unsupported completion shell")
}

func TestRunCLICompletionRejectsMissingShell(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"completion"}, &out)
	assertUsageError(t, err, out.String(), "completion requires exactly one shell")
}

func TestRunCLICompletionHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"help", "completion"}, &out); err != nil {
		t.Fatalf("completion help failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{"tunwarden completion bash", "tunwarden completion zsh", "tunwarden completion fish", "does not contact tunwardend"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected completion help to contain %q, got %q", want, got)
		}
	}
}

func TestRunCLICompletionRuntimeSuggestsProfileIDs(t *testing.T) {
	opts := seedCompletionStores(t)
	tests := []struct {
		name string
		args []string
	}{
		{name: "connect", args: bashCompleteArgs(2, "tunwarden", "connect", "")},
		{name: "plan", args: bashCompleteArgs(4, "tunwarden", "plan", "--mode", "tun", "")},
		{name: "profile show", args: bashCompleteArgs(3, "tunwarden", "profile", "show", "")},
		{name: "profile delete", args: bashCompleteArgs(3, "tunwarden", "profile", "delete", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCompletionRuntime(t, opts, tt.args...)
			assertContainsLine(t, got, "alpha")
			assertContainsLine(t, got, "bravo")
			assertNotContainsLine(t, got, "personal")
		})
	}
}

func TestRunCLICompletionRuntimeSuggestsSubscriptionIDs(t *testing.T) {
	opts := seedCompletionStores(t)
	tests := []struct {
		name string
		args []string
	}{
		{name: "subscription show", args: bashCompleteArgs(3, "tunwarden", "subscription", "show", "")},
		{name: "subscription update", args: bashCompleteArgs(3, "tunwarden", "subscription", "update", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCompletionRuntime(t, opts, tt.args...)
			assertContainsLine(t, got, "personal")
			assertContainsLine(t, got, "work")
			assertNotContainsLine(t, got, "alpha")
		})
	}
}

func TestRunCLICompletionRuntimeSuggestsStaticValues(t *testing.T) {
	opts := seedCompletionStores(t)
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "mode values",
			args: bashCompleteArgs(3, "tunwarden", "plan", "--mode", ""),
			want: []string{"proxy-only", "tun"},
		},
		{
			name: "protocol values",
			args: bashCompleteArgs(4, "tunwarden", "profile", "add", "--protocol", ""),
			want: []string{"vless", "vmess", "trojan", "shadowsocks"},
		},
		{
			name: "top level commands",
			args: bashCompleteArgs(1, "tunwarden", ""),
			want: []string{"connect", "completion", "profile", "subscription"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCompletionRuntime(t, opts, tt.args...)
			for _, want := range tt.want {
				assertContainsLine(t, got, want)
			}
		})
	}
}

func TestRunCLICompletionRuntimeDoesNotSuggestUsedNonRepeatableFlags(t *testing.T) {
	opts := seedCompletionStores(t)
	got := runCompletionRuntime(t, opts, bashCompleteArgs(4, "tunwarden", "plan", "--mode", "tun", "-")...)
	assertContainsLine(t, got, "--json")
	assertNotContainsLine(t, got, "--mode")
}

func TestRunCLICompletionRuntimeUsesDefaultFilesForImportPath(t *testing.T) {
	got := runCompletionRuntime(t, options{}, bashCompleteArgs(2, "tunwarden", "import", "")...)
	assertContainsLine(t, got, ":default-files")
	assertNotContainsLine(t, got, ":no-files")
}

func TestRunCLICompletionRuntimeMissingOrUnreadableStateIsQuiet(t *testing.T) {
	dir := t.TempDir()
	missing := options{profileStorePath: filepath.Join(dir, "missing-profiles.json"), subscriptionStorePath: filepath.Join(dir, "missing-subscriptions.json")}
	got := runCompletionRuntime(t, missing, bashCompleteArgs(2, "tunwarden", "connect", "")...)
	assertContainsLine(t, got, ":no-files")
	assertNotContainsLine(t, got, "alpha")

	unreadableProfileStore := filepath.Join(dir, "unreadable-profiles.json")
	if err := os.WriteFile(unreadableProfileStore, []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write unreadable profile store fixture: %v", err)
	}
	unreadable := options{profileStorePath: unreadableProfileStore, subscriptionStorePath: filepath.Join(dir, "missing-subscriptions.json")}
	got = runCompletionRuntime(t, unreadable, bashCompleteArgs(2, "tunwarden", "connect", "")...)
	assertContainsLine(t, got, ":no-files")
	assertNotContainsLine(t, got, "not-json")
}

func seedCompletionStores(t *testing.T) options {
	t.Helper()
	dir := t.TempDir()
	profileStore, err := profile.NewStore(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatalf("create profile store: %v", err)
	}
	for _, p := range []profile.Profile{
		profile.NewManual("Alpha", "alpha.example", 443, "vless"),
		profile.NewManual("Bravo", "bravo.example", 443, "trojan"),
	} {
		if err := profileStore.Add(p); err != nil {
			t.Fatalf("seed profile %s: %v", p.ID, err)
		}
	}

	subscriptionStore, err := sub.NewStore(filepath.Join(dir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("create subscription store: %v", err)
	}
	for _, source := range []sub.Source{
		sub.NewSource("Personal", "file:///tmp/personal-subscription.txt"),
		sub.NewSource("Work", "file:///tmp/work-subscription.txt"),
	} {
		if err := subscriptionStore.Add(source); err != nil {
			t.Fatalf("seed subscription %s: %v", source.ID)
		}
	}

	return options{profileStorePath: profileStore.Path(), subscriptionStorePath: subscriptionStore.Path()}
}

func bashCompleteArgs(cursor int, words ...string) []string {
	args := []string{"bash", strconv.Itoa(cursor)}
	return append(args, words...)
}

func runCompletionRuntime(t *testing.T, opts options, args ...string) []string {
	t.Helper()
	var out bytes.Buffer
	allArgs := append([]string{"__complete"}, args...)
	if err := runWithOptions(context.Background(), allArgs, &out, opts); err != nil {
		t.Fatalf("completion runtime failed: %v", err)
	}
	return splitLines(out.String())
}

func splitLines(raw string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func assertContainsLine(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, line := range lines {
		if line == want {
			return
		}
	}
	t.Fatalf("expected lines to contain %q, got %#v", want, lines)
}

func assertNotContainsLine(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, line := range lines {
		if line == want {
			t.Fatalf("expected lines not to contain %q, got %#v", want, lines)
		}
	}
}
