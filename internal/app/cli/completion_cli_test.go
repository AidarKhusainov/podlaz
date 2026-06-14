package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
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
			want: []string{"_tunwarden()", "complete -F _tunwarden tunwarden", "proxy-only tun", "--protocol"},
		},
		{
			name: "zsh",
			args: []string{"completion", "zsh"},
			want: []string{"#compdef tunwarden", "_tunwarden \"$@\"", "'proxy-only' 'tun'", "--protocol"},
		},
		{
			name: "fish",
			args: []string{"completion", "fish"},
			want: []string{"complete -c tunwarden -f", "__fish_tunwarden_using_command connect", "-a 'proxy-only tun'", "-l protocol"},
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
