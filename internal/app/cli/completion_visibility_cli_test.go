package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCompletionSetupHiddenFromEverydayHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"--help"}} {
		var out bytes.Buffer
		if err := run(context.Background(), args, &out); err != nil {
			t.Fatalf("help %v: %v", args, err)
		}
		if strings.Contains(out.String(), "podlaz completion") {
			t.Fatalf("help %v advertises setup: %q", args, out.String())
		}
		for _, want := range []string{"podlaz connect", "podlaz status", "podlaz debug"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("help %v missing %q", args, want)
			}
		}
	}
}

func TestCompletionSetupHiddenFromSuggestionsButExplicitlyAvailable(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		for _, executable := range []string{"podlaz", "plz"} {
			got := runCompletionRuntime(t, options{}, shellCompleteArgs(shell, 1, executable, "")...)
			assertNotContainsCandidateValue(t, got, "completion")
			assertContainsCandidateValue(t, got, "connect")
			got = runCompletionRuntime(t, options{}, shellCompleteArgs(shell, 2, executable, "completion", "")...)
			for _, want := range []string{"bash", "zsh", "fish"} {
				assertContainsCandidateValue(t, got, want)
			}
			got = runCompletionRuntime(t, options{}, shellCompleteArgs(shell, 2, executable, "help", "")...)
			assertNotContainsCandidateValue(t, got, "completion")
		}
	}

	for _, args := range [][]string{{"help", "completion"}, {"completion", "--help"}} {
		var out bytes.Buffer
		if err := run(context.Background(), args, &out); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out.String(), "podlaz completion bash") {
			t.Fatalf("%v missing completion help: %q", args, out.String())
		}
	}
	var out bytes.Buffer
	err := run(context.Background(), []string{"completion"}, &out)
	if ExitCode(err) != 2 || out.Len() != 0 {
		t.Fatalf("missing shell must be usage error, err=%v stdout=%q", err, out.String())
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out.Reset()
		if err := run(context.Background(), []string{"completion", shell}, &out); err != nil {
			t.Fatalf("explicit completion %s: %v", shell, err)
		}
		if !strings.Contains(out.String(), "__complete "+shell) {
			t.Fatalf("missing runtime dispatch in generated %s script", shell)
		}
		if strings.Contains(strings.SplitN(out.String(), "\n", 3)[1], "completion ") {
			t.Fatalf("generated %s command summary advertises setup", shell)
		}
	}
}
