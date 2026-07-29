package recovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvedMissingDeviceResultUsesRawProductionOSRunnerOutput(t *testing.T) {
	tests := []struct {
		name          string
		stdoutCommand string
		stderrCommand string
		wantMissing   bool
	}{
		{
			name:          "unterminated stderr",
			stdoutCommand: ":",
			stderrCommand: `printf '%s' 'Failed to resolve interface "podlaz0": No such device' >&2`,
		},
		{
			name:          "exact LF",
			stdoutCommand: ":",
			stderrCommand: `printf '%s\n' 'Failed to resolve interface "podlaz0": No such device' >&2`,
			wantMissing:   true,
		},
		{
			name:          "exact CRLF",
			stdoutCommand: ":",
			stderrCommand: `printf '%s\r\n' 'Failed to resolve interface "podlaz0": No such device' >&2`,
			wantMissing:   true,
		},
		{
			name:          "whitespace stdout",
			stdoutCommand: `printf ' \n'`,
			stderrCommand: `printf '%s\n' 'Failed to resolve interface "podlaz0": No such device' >&2`,
		},
		{
			name:          "padded stderr",
			stdoutCommand: ":",
			stderrCommand: `printf ' %s \n' 'Failed to resolve interface "podlaz0": No such device' >&2`,
		},
		{
			name:          "additional blank stderr line",
			stdoutCommand: ":",
			stderrCommand: `printf '%s\n\n' 'Failed to resolve interface "podlaz0": No such device' >&2`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeRecoveryResolvedProtocolScript(t, tt.stdoutCommand, tt.stderrCommand)
			ctx := context.Background()
			result, err := (OSRunner{}).Run(ctx, path)
			if got := resolvedMissingDeviceResult(ctx, result, err); got != tt.wantMissing {
				t.Fatalf("resolved missing classification = %v, want %v; result=%#v err=%v", got, tt.wantMissing, result, err)
			}
		})
	}
}

func writeRecoveryResolvedProtocolScript(t *testing.T, stdoutCommand, stderrCommand string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "resolvectl")
	script := "#!/bin/sh\nset -eu\n" + stdoutCommand + "\n" + stderrCommand + "\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write recovery resolvectl protocol fixture: %v", err)
	}
	return path
}
