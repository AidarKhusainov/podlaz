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

func TestRunCLICompletionGeneratesRuntimeDrivenSupportedShells(t *testing.T) {
	tests := []struct {
		shell string
		want  []string
	}{
		{shell: "bash", want: []string{"__complete bash", "complete -o default -F _podlaz podlaz plz"}},
		{shell: "zsh", want: []string{"#compdef podlaz plz", "__complete zsh"}},
		{shell: "fish", want: []string{"__complete fish", "complete -c podlaz -f", "complete -c plz -f"}},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			var out bytes.Buffer
			if err := run(context.Background(), []string{"completion", tt.shell}, &out); err != nil {
				t.Fatalf("completion %s: %v", tt.shell, err)
			}
			got := out.String()
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("completion %s missing %q: %q", tt.shell, want, got)
				}
			}
			for _, forbidden := range []string{"podlaz plan", "profile validate", "--handoff", "proxy-only tun"} {
				if strings.Contains(got, forbidden) {
					t.Fatalf("completion %s leaked obsolete surface %q: %q", tt.shell, forbidden, got)
				}
			}
		})
	}
}

func TestRunCLIBashCompletionNeverAppendsRuntimeDescriptionsToValues(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		`value="${line%%$'\t'*}"`,
		`values+=("$value")`,
		`COMPREPLY=("${values[@]}")`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("bash completion missing %q: %q", want, got)
		}
	}
}

func TestRunCLIBashCompletionScriptKeepsCOMPREPLYInsertable(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"completion", "bash"}, &out); err != nil {
		t.Fatal(err)
	}
	got := runGeneratedBashCompletion(t, out.String(), "described-profile", "podlaz", "profile", "")
	want := []string{"list", "show", "use", "delete"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("COMPREPLY want=%#v got=%#v", want, got)
	}
}

func TestRunCLICompletionRejectsUnsupportedOrMissingShell(t *testing.T) {
	for _, args := range [][]string{{"completion"}, {"completion", "powershell"}} {
		var out bytes.Buffer
		err := run(context.Background(), args, &out)
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("args=%v err=%v exit=%d", args, err, ExitCode(err))
		}
	}
}

func TestRunCLICompletionHelpExplainsHumanProfileSelectors(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"help", "completion"}, &out); err != nil {
		t.Fatal(err)
	}
	got := strings.ToLower(out.String())
	for _, want := range []string{"podlaz completion bash", "prefers human", "stable ids", "plz"} {
		if !strings.Contains(got, strings.ToLower(want)) {
			t.Fatalf("completion help missing %q: %q", want, out.String())
		}
	}
}

func TestRunCLICompletionRuntimeSuggestsHumanProfileNames(t *testing.T) {
	opts := seedCompletionStores(t)
	for _, words := range [][]string{
		{"podlaz", "connect", ""},
		{"podlaz", "profile", "show", ""},
		{"podlaz", "profile", "use", ""},
		{"podlaz", "profile", "delete", ""},
	} {
		got := runCompletionRuntime(t, opts, bashCompleteArgs(len(words)-1, words...)...)
		assertContainsCandidateValue(t, got, "Alpha")
		assertContainsCandidateValue(t, got, "Bravo")
		assertNotContainsCandidateValue(t, got, "alpha")
	}
}

func TestRunCLICompletionRuntimeSuggestsSubscriptionIDs(t *testing.T) {
	opts := seedCompletionStores(t)
	for _, command := range []string{"show", "update", "delete"} {
		got := runCompletionRuntime(t, opts, bashCompleteArgs(3, "podlaz", "subscription", command, "")...)
		assertContainsCandidateValue(t, got, "personal")
		assertContainsCandidateValue(t, got, "work")
		assertNotContainsCandidateValue(t, got, "Alpha")
	}
}

func TestRunCLICompletionRuntimeUsesProgressiveTopLevelDescriptions(t *testing.T) {
	got := runCompletionRuntime(t, options{}, bashCompleteArgs(1, "podlaz", "")...)
	assertContainsCandidateLine(t, got, "connect", "Connect full VPN")
	assertContainsCandidateLine(t, got, "profile", "Manage profiles")
	assertContainsCandidateLine(t, got, "debug", "Advanced diagnostics")
	for _, forbidden := range []string{"plan", "check", "doctor", "logs", "recover"} {
		assertNotContainsCandidateValue(t, got, forbidden)
	}
}

func TestRunCLICompletionRuntimeUsesDefaultFilesForCanonicalImport(t *testing.T) {
	got := runCompletionRuntime(t, options{}, bashCompleteArgs(2, "podlaz", "import", "")...)
	assertContainsLine(t, got, ":default-files")
	assertNotContainsLine(t, got, ":no-files")
}

func TestRunCLICompletionRuntimeMissingOrUnreadableStateIsQuiet(t *testing.T) {
	dir := t.TempDir()
	missing := options{
		profileStorePath:      filepath.Join(dir, "missing-profiles.json"),
		subscriptionStorePath: filepath.Join(dir, "missing-subscriptions.json"),
	}
	got := runCompletionRuntime(t, missing, bashCompleteArgs(2, "podlaz", "connect", "")...)
	assertContainsLine(t, got, ":no-files")
	assertNotContainsCandidateValue(t, got, "Alpha")

	badPath := filepath.Join(dir, "bad-profiles.json")
	if err := os.WriteFile(badPath, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := options{profileStorePath: badPath}
	got = runCompletionRuntime(t, bad, bashCompleteArgs(2, "podlaz", "connect", "")...)
	assertContainsLine(t, got, ":no-files")
	assertNotContainsLine(t, got, "not-json")
}

func seedCompletionStores(t *testing.T) options {
	t.Helper()
	dir := t.TempDir()
	profileStore, err := profile.NewStore(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	alpha := testConnectProfile()
	alpha.ID, alpha.Name, alpha.Protocol = "alpha", "Alpha", "vless"
	bravo := testConnectProfile()
	bravo.ID, bravo.Name, bravo.Protocol = "bravo", "Bravo", "trojan"
	for _, p := range []profile.Profile{alpha, bravo} {
		if err := profileStore.Add(p); err != nil {
			t.Fatal(err)
		}
	}

	subscriptionStore, err := sub.NewStore(filepath.Join(dir, "subscriptions.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []sub.Source{
		sub.NewSource("Personal", "file:///tmp/personal-subscription.txt"),
		sub.NewSource("Work", "file:///tmp/work-subscription.txt"),
	} {
		if err := subscriptionStore.Add(source); err != nil {
			t.Fatal(err)
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

func runGeneratedBashCompletion(t *testing.T, completionScript, fixture string, words ...string) []string {
	t.Helper()
	dir := t.TempDir()
	completionPath := filepath.Join(dir, "podlaz.bash")
	if err := os.WriteFile(completionPath, []byte(completionScript), 0o600); err != nil {
		t.Fatal(err)
	}
	fakePodlazPath := filepath.Join(dir, "podlaz")
	if err := os.WriteFile(fakePodlazPath, []byte(fakePodlazRuntimeCompletionScript), 0o700); err != nil {
		t.Fatal(err)
	}

	var driver strings.Builder
	driver.WriteString("source ")
	driver.WriteString(strconv.Quote(completionPath))
	driver.WriteString("\nCOMP_WORDS=(")
	driver.WriteString(shellWords(words))
	driver.WriteString(")\nCOMP_CWORD=")
	driver.WriteString(strconv.Itoa(len(words) - 1))
	driver.WriteString("\nCOMP_TYPE=63\n_podlaz\nprintf '%s\\n' \"${COMPREPLY[@]}\"\n")

	cmd := exec.Command("bash", "-c", driver.String())
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PODLAZ_COMPLETION_FIXTURE="+fixture,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated bash completion: %v: %s", err, output)
	}
	return splitLines(string(output))
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
    printf 'list\tList profiles\n'
    printf 'show\tShow profile\n'
    printf 'use\tSelect profile\n'
    printf 'delete\tDelete profile\n'
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
	t.Fatalf("expected %q in %#v", want, lines)
}

func assertNotContainsLine(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, line := range lines {
		if line == want {
			t.Fatalf("unexpected %q in %#v", want, lines)
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

func assertContainsCandidateValue(t *testing.T, lines []string, value string) {
	t.Helper()
	for _, line := range lines {
		candidateValue, _, _ := strings.Cut(line, "\t")
		if candidateValue == value {
			return
		}
	}
	t.Fatalf("expected candidate value %q in %#v", value, lines)
}

func assertNotContainsCandidateValue(t *testing.T, lines []string, value string) {
	t.Helper()
	for _, line := range lines {
		candidateValue, _, _ := strings.Cut(line, "\t")
		if candidateValue == value {
			t.Fatalf("unexpected candidate value %q in %#v", value, lines)
		}
	}
}
