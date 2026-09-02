package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestFindForeignRouteOnlyDNSOwnerIgnoresCurrentScopesWithoutConfigurationEvidence(t *testing.T) {
	for _, currentScopes := range [][]string{{"none"}, {"DNS"}} {
		links := []netsnapshot.ResolvedLink{{
			Name:          "wg-example",
			CurrentScopes: currentScopes,
			DNSDomains:    []string{resolvedRouteOnlyDomain},
		}}

		if foreign, ok := findForeignRouteOnlyDNSOwner(links, "podlaz0"); ok {
			t.Fatalf("Current Scopes %v must not establish foreign DNS ownership: %#v", currentScopes, foreign)
		}
	}
}

func TestFindForeignRouteOnlyDNSOwnerUsesStableConfigurationRegardlessOfCurrentScopes(t *testing.T) {
	for _, currentScopes := range [][]string{{"none"}, {"DNS"}} {
		links := []netsnapshot.ResolvedLink{{
			Name:          "wg-example",
			CurrentScopes: currentScopes,
			Protocols:     []string{"+DefaultRoute"},
			DNSDomains:    []string{resolvedRouteOnlyDomain},
		}}

		foreign, ok := findForeignRouteOnlyDNSOwner(links, "podlaz0")
		if !ok || foreign.Name != "wg-example" {
			t.Fatalf("stable route-only DNS configuration must establish ownership for Current Scopes %v: %#v, %v", currentScopes, foreign, ok)
		}
	}
}

func TestResolvedMissingMatcherUsesRawProductionOSRunnerOutput(t *testing.T) {
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
			installResolvedProtocolScript(t, tt.stdoutCommand, tt.stderrCommand)
			ctx := context.Background()
			result, err := observeCommand(ctx, OSRunner{}, "resolvectl", "revert", "podlaz0")
			if got := resolvedCommandResultIsMissing(ctx, result, err); got != tt.wantMissing {
				t.Fatalf("resolved missing classification = %v, want %v; result=%#v err=%v", got, tt.wantMissing, result, err)
			}
		})
	}
}

func installResolvedProtocolScript(t *testing.T, stdoutCommand, stderrCommand string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "resolvectl")
	script := "#!/bin/sh\nset -eu\n" + stdoutCommand + "\n" + stderrCommand + "\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write resolvectl protocol fixture: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
