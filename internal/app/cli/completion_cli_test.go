package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/sub"
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
			want: []string{"_podlaz()", "__complete bash", "complete -o default -F _podlaz podlaz", "proxy-only tun", "vless vmess trojan shadowsocks", "COMPREPLY=(\"${values[@]}\")", "value-only display fallback"},
		},
		{
			name: "zsh",
			args: []string{"completion", "zsh"},
			want: []string{"#compdef podlaz", "__complete zsh", "_describe -t podlaz-completions", "_podlaz \"$@\"", "proxy-only tun", "vless vmess trojan shadowsocks"},
		},
		{
			name: "fish",
			args: []string{"completion", "fish"},
			want: []string{"complete -c podlaz -f", "__complete fish", "__fish_podlaz_using_command plan", "-l mode -x -a 'proxy-only tun'", "-l protocol -x -a 'vless vmess trojan shadowsocks'", "-l follow -s f"},
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

func TestRunCLIBashCompletionNeverAppendsRawProtocolLines(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &out); err != nil {
		t.Fatalf("completion command failed: %v", err)
	}
	got := out.String()
	for _, forbidden := range []string{
		`matches+=("$line")`,
		`COMPREPLY+=("$line")`,
		`COMPREPLY+=("${line}")`,
		`COMPREPLY=("${matches[@]}")`,
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("bash completion must not append raw runtime protocol lines to COMPREPLY; found %q in:\n%s", forbidden, got)
		}
	}
	for _, want := range []string{
		`value="${line%%$'\t'*}"`,
		`values+=("$value")`,
		`COMPREPLY=("${values[@]}")`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected bash completion to contain %q, got:\n%s", want, got)
		}
	}
}

func TestRunCLIBashCompletionScriptKeepsCOMPREPLYInsertable(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}

	var out bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &out); err != nil {
		t.Fatalf("completion command failed: %v", err)
	}
	script := out.String()

	tests := []struct {
		name    string
		fixture string
		words   []string
		want    []string
	}{
		{
			name:    "described profile subcommands",
			fixture: "described-profile",
			words:   []string{"podlaz", "profile", ""},
			want:    []string{"add", "delete", "import", "list", "show", "validate"},
		},
		{
			name:    "described top level commands",
			fixture: "described-top-level",
			words:   []string{"podlaz", ""},
			want:    []string{"profile", "connect", "status"},
		},
		{
			name:    "described flags",
			fixture: "described-flags",
			words:   []string{"podlaz", "plan", "-"},
			want:    []string{"--mode", "--json"},
		},
		{
			name:    "dynamic IDs without descriptions",
			fixture: "plain-dynamic",
			words:   []string{"podlaz", "connect", ""},
			want:    []string{"alpha", "bravo"},
		},
		{
			name:    "no files directive with plain values",
			fixture: "no-files",
			words:   []string{"podlaz", "connect", ""},
			want:    []string{"alpha"},
		},
		{
			name:    "default files directive returns without values",
			fixture: "default-files",
			words:   []string{"podlaz", "import", ""},
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runGeneratedBashCompletion(t, script, tt.fixture, tt.words...)
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("unexpected COMPREPLY values\nwant: %#v\n got: %#v", tt.want, got)
			}
			for _, value := range got {
				if strings.Contains(value, "\t") {
					t.Fatalf("COMPREPLY value must not contain raw runtime description protocol tab: %#v", got)
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
	for _, want := range []string{"podlaz completion bash", "podlaz completion zsh", "podlaz completion fish", "packaged plz alias", "does not contact podlazd"} {
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
		{name: "connect", args: bashCompleteArgs(2, "podlaz", "connect", "")},
		{name: "plan", args: bashCompleteArgs(4, "podlaz", "plan", "--mode", "tun", "")},
		{name: "profile show", args: bashCompleteArgs(3, "podlaz", "profile", "show", "")},
		{name: "profile delete", args: bashCompleteArgs(3, "podlaz", "profile", "delete", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCompletionRuntime(t, opts, tt.args...)
			assertContainsCandidateLine(t, got, "alpha", "Alpha")
			assertContainsCandidateLine(t, got, "bravo", "Bravo")
			assertNotContainsCandidateValue(t, got, "personal")
		})
	}
}

func TestRunCLICompletionRuntimeSuggestsSubscriptionIDs(t *testing.T) {
	opts := seedCompletionStores(t)
	tests := []struct {
		name string
		args []string
	}{
		{name: "subscription show", args: bashCompleteArgs(3, "podlaz", "subscription", "show", "")},
		{name: "subscription update", args: bashCompleteArgs(3, "podlaz", "subscription", "update", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCompletionRuntime(t, opts, tt.args...)
			assertContainsCandidateLine(t, got, "personal", "Personal")
			assertContainsCandidateLine(t, got, "work", "Work")
			assertNotContainsCandidateValue(t, got, "alpha")
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
			args: bashCompleteArgs(3, "podlaz", "plan", "--mode", ""),
			want: []string{"proxy-only", "tun"},
		},
		{
			name: "protocol values",
			args: bashCompleteArgs(4, "podlaz", "profile", "add", "--protocol", ""),
			want: []string{"vless", "vmess", "trojan", "shadowsocks"},
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

func TestRunCLICompletionRuntimeSuggestsCommandDescriptions(t *testing.T) {
	opts := seedCompletionStores(t)
	got := runCompletionRuntime(t, opts, bashCompleteArgs(1, "podlaz", "")...)
	assertContainsCandidateLine(t, got, "connect", "Start connection")
	assertContainsCandidateLine(t, got, "profile", "Manage profiles")
	assertContainsCandidateLine(t, got, "subscription", "Manage subscriptions")
}

func TestRunCLICompletionRuntimeSuggestsProfileSubcommandDescriptions(t *testing.T) {
	opts := seedCompletionStores(t)
	got := runCompletionRuntime(t, opts, bashCompleteArgs(2, "podlaz", "profile", "")...)
	assertContainsCandidateLine(t, got, "add", "Add manual profile")
	assertContainsCandidateLine(t, got, "delete", "Delete profile")
	assertContainsCandidateLine(t, got, "validate", "Validate profile")
}

func TestRunCLICompletionRuntimeSuggestsFlagDescriptions(t *testing.T) {
	opts := seedCompletionStores(t)
	got := runCompletionRuntime(t, opts, bashCompleteArgs(2, "podlaz", "plan", "-")...)
	assertContainsCandidateLine(t, got, "--mode", "Select connection mode")
	assertContainsCandidateLine(t, got, "--json", "Print JSON output")
}

func TestRunCLICompletionRuntimeDoesNotSuggestUsedNonRepeatableFlags(t *testing.T) {
	opts := seedCompletionStores(t)
	got := runCompletionRuntime(t, opts, bashCompleteArgs(4, "podlaz", "plan", "--mode", "tun", "-")...)
	assertContainsCandidateLine(t, got, "--json", "Print JSON output")
	assertNotContainsCandidateValue(t, got, "--mode")
}

func TestRunCLICompletionRuntimeUsesDefaultFilesForImportPath(t *testing.T) {
	got := runCompletionRuntime(t, options{}, bashCompleteArgs(2, "podlaz", "import", "")...)
	assertContainsLine(t, got, ":default-files")
	assertNotContainsLine(t, got, ":no-files")
}

func TestRunCLICompletionRuntimeMissingOrUnreadableStateIsQuiet(t *testing.T) {
	dir := t.TempDir()
	missing := options{profileStorePath: filepath.Join(dir, "missing-profiles.json"), subscriptionStorePath: filepath.Join(dir, "missing-subscriptions.json")}
	got := runCompletionRuntime(t, missing, bashCompleteArgs(2, "podlaz", "connect", "")...)
	assertContainsLine(t, got, ":no-files")
	assertNotContainsCandidateValue(t, got, "alpha")

	unreadableProfileStore := filepath.Join(dir, "unreadable-profiles.json")
	if err := os.WriteFile(unreadableProfileStore, []byte("not-json"), 0o600); err != nil {
		t.Fatalf("write unreadable profile store fixture: %v", err)
	}
	unreadable := options{profileStorePath: unreadableProfileStore, subscriptionStorePath: filepath.Join(dir, "missing-subscriptions.json")}
	got = runCompletionRuntime(t, unreadable, bashCompleteArgs(2, "podlaz", "connect", "")...)
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
			t.Fatalf("seed subscription %s: %v", source.ID, err)
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

func runGeneratedBashCompletion(t *testing.T, completionScript string, fixture string, words ...string) []string {
	t.Helper()
	dir := t.TempDir()
	completionPath := filepath.Join(dir, "podlaz.bash")
	if err := os.WriteFile(completionPath, []byte(completionScript), 0o600); err != nil {
		t.Fatalf("write generated bash completion: %v", err)
	}
	fakePodlazPath := filepath.Join(dir, "podlaz")
	if err := os.WriteFile(fakePodlazPath, []byte(fakePodlazRuntimeCompletionScript), 0o700); err != nil {
		t.Fatalf("write fake podlaz runtime completion: %v", err)
	}

	var driver strings.Builder
	driver.WriteString("source ")
	driver.WriteString(strconv.Quote(completionPath))
	driver.WriteString("\n")
	driver.WriteString("COMP_WORDS=(")
	driver.WriteString(shellWords(words))
	driver.WriteString(")\n")
	driver.WriteString("COMP_CWORD=")
	driver.WriteString(strconv.Itoa(len(words) - 1))
	driver.WriteString("\n")
	driver.WriteString("COMP_TYPE=63\n")
	driver.WriteString("_podlaz\n")
	driver.WriteString("printf '%s\\n' \"${COMPREPLY[@]}\"\n")

	cmd := exec.Command("bash", "-c", driver.String())
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PODLAZ_COMPLETION_FIXTURE="+fixture,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run generated bash completion: %v\nscript:\n%s\noutput:\n%s", err, driver.String(), out)
	}
	return splitLines(string(out))
}

func shellWords(words []string) string {
	quoted := make([]string, 0, len(words))
	for _, word := range words {
		quoted = append(quoted, strconv.Quote(word))
	}
	return strings.Join(quoted, " ")
}

const fakePodlazRuntimeCompletionScript = `#!/usr/bin/env bash
set -euo pipefail

if [ "${1:-}" != "__complete" ]; then
  exit 64
fi

case "${PODLAZ_COMPLETION_FIXTURE:-}" in
  described-profile)
    printf '%s\n' ':no-files'
    printf 'add\tAdd manual profile\n'
    printf 'delete\tDelete profile\n'
    printf 'import\tImport share URI\n'
    printf 'list\tList profiles\n'
    printf 'show\tShow profile\n'
    printf 'validate\tValidate profile\n'
    ;;
  described-top-level)
    printf '%s\n' ':no-files'
    printf 'profile\tManage profiles\n'
    printf 'connect\tStart connection\n'
    printf 'status\tShow status\n'
    ;;
  described-flags)
    printf '%s\n' ':no-files'
    printf -- '--mode\tSelect connection mode\n'
    printf -- '--json\tPrint JSON output\n'
    ;;
  plain-dynamic)
    printf '%s\n' ':no-files' alpha bravo
    ;;
  no-files)
    printf '%s\n' ':no-files' alpha
    ;;
  default-files)
    printf '%s\n' ':default-files'
    ;;
  *)
    exit 65
    ;;
esac
`

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

func assertContainsCandidateLine(t *testing.T, lines []string, value, description string) {
	t.Helper()
	want := value
	if description != "" {
		want += "\t" + description
	}
	assertContainsLine(t, lines, want)
}

func assertNotContainsCandidateValue(t *testing.T, lines []string, value string) {
	t.Helper()
	for _, line := range lines {
		candidateValue, _, _ := strings.Cut(line, "\t")
		if candidateValue == value {
			t.Fatalf("expected candidate value %q to be absent, got %#v", value, lines)
		}
	}
}
