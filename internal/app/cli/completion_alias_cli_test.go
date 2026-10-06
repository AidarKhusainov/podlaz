package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunCLICompletionGeneratesPlzAliasSupport(t *testing.T) {
	for _, tt := range []struct {
		shell string
		want  string
	}{
		{shell: "bash", want: "complete -o default -F _podlaz podlaz plz"},
		{shell: "zsh", want: "#compdef podlaz plz"},
		{shell: "fish", want: "complete -c plz -f"},
	} {
		var out bytes.Buffer
		if err := run(context.Background(), []string{"completion", tt.shell}, &out); err != nil {
			t.Fatalf("completion %s: %v", tt.shell, err)
		}
		if !strings.Contains(out.String(), tt.want) {
			t.Fatalf("completion %s missing %q", tt.shell, tt.want)
		}
	}
}

func TestRunCLICompletionRuntimeAcceptsPlzCommandName(t *testing.T) {
	got := runCompletionRuntime(t, options{}, bashCompleteArgs(1, "plz", "")...)
	assertContainsCandidateLine(t, got, "connect", "Connect full VPN")
	assertContainsCandidateLine(t, got, "completion", "Generate completion")
	assertContainsCandidateLine(t, got, "debug", "Advanced diagnostics")
}
