package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/client"
	"github.com/AidarKhusainov/podlaz/internal/daemon"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	statuspkg "github.com/AidarKhusainov/podlaz/internal/status"
)

func TestExitZeroResolvedMissingComposesThroughDaemonStatusRecoverDryRunAndRecoveryExecute(t *testing.T) {
	runtimeDir := t.TempDir()
	fakeBinDir := t.TempDir()
	resolvedCallsPath := filepath.Join(t.TempDir(), "resolved-status-calls")

	writeRecoverJSONFakeCommand(t, fakeBinDir, "ip", `#!/bin/sh
printf '%s\n' 'Device "podlaz0" does not exist.' >&2
exit 1
`)
	writeRecoverJSONFakeCommand(t, fakeBinDir, "nft", `#!/bin/sh
printf '%s\n' 'Error: No such file or directory' >&2
exit 1
`)
	writeRecoverJSONFakeCommand(t, fakeBinDir, "resolvectl", `#!/bin/sh
set -eu
if [ "$#" -ne 3 ] || [ "$1" != "status" ] || [ "$2" != "podlaz0" ] || [ "$3" != "--no-pager" ]; then
  printf '%s\n' 'unexpected resolvectl invocation' >&2
  exit 64
fi
: "${RECOVER_JSON_RESOLVED_CALLS:?}"
printf '%s\n' status >>"${RECOVER_JSON_RESOLVED_CALLS}"
printf '%s\n' 'Failed to resolve interface "podlaz0", ignoring: No such device' >&2
exit 0
`)

	t.Setenv("PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RECOVER_JSON_RESOLVED_CALLS", resolvedCallsPath)
	t.Setenv(api.RuntimeDirEnv, runtimeDir)
	t.Setenv(api.ServiceEnv, api.ServiceManual)

	serverCtx, cancelServer := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- (daemon.Server{RuntimeDir: runtimeDir}).Run(serverCtx)
	}()
	t.Cleanup(func() {
		cancelServer()
		select {
		case err := <-serverDone:
			if err != nil {
				t.Errorf("recover JSON daemon shutdown failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("recover JSON daemon did not stop")
		}
	})

	socketPath := filepath.Join(runtimeDir, api.SocketName)
	statusClient := client.StatusClient{SocketPath: socketPath, Timeout: time.Second}
	initial, err := statusClient.Status(context.Background())
	if err != nil {
		t.Fatalf("read initial daemon status: %v", err)
	}
	assertRecoverJSONDaemonStartupScanClean(t, initial)

	statusOpts := options{
		daemonStatus: func(ctx context.Context) (statuspkg.Report, error) {
			response, err := statusClient.Status(ctx)
			if err != nil {
				return statuspkg.Report{}, err
			}
			return statuspkg.FromDaemon(response), nil
		},
	}
	assertRecoverJSONStatusCommandClean(t, statusOpts)

	var dryRunOut bytes.Buffer
	err = runRecoverCommand(context.Background(), []string{"--json"}, &dryRunOut, options{})
	if got := ExitCode(err); got != 0 {
		t.Fatalf("recover --json must exit 0 for exact exit-0 missing-link state: code=%d err=%v output=%q", got, err, dryRunOut.String())
	}
	assertRecoverJSONDryRunClean(t, dryRunOut.Bytes())

	beforeRecovery := recoverJSONResolvedStatusCallCount(t, resolvedCallsPath)
	if beforeRecovery < 1 {
		t.Fatalf("expected startup scan to execute resolvectl status, got %d calls", beforeRecovery)
	}

	recoveryClient := client.RecoveryClient{
		SocketPath:       socketPath,
		DialTimeout:      time.Second,
		OperationTimeout: 5 * time.Second,
	}
	var recoverOut bytes.Buffer
	err = runRecoverCommand(context.Background(), []string{"--execute", "--yes", "--json"}, &recoverOut, options{
		recoverExecute: func(ctx context.Context) (recovery.ExecuteResult, error) {
			response, err := recoveryClient.Recover(ctx)
			if err != nil {
				return recovery.ExecuteResult{}, err
			}
			return recoveryResultFromAPI(response), nil
		},
	})
	if got := ExitCode(err); got != 0 {
		t.Fatalf("recover execute must exit 0 for exact exit-0 missing-link state: code=%d err=%v output=%q", got, err, recoverOut.String())
	}
	assertRecoverJSONExecuteClean(t, recoverOut.Bytes())

	afterRecovery := recoverJSONResolvedStatusCallCount(t, resolvedCallsPath)
	if afterRecovery < beforeRecovery+2 {
		t.Fatalf("recover execute must perform its inspection and a fresh authoritative startup-scan refresh: before=%d after=%d", beforeRecovery, afterRecovery)
	}

	postRecovery, err := statusClient.Status(context.Background())
	if err != nil {
		t.Fatalf("read post-recovery daemon status: %v", err)
	}
	assertRecoverJSONDaemonStartupScanClean(t, postRecovery)
	assertRecoverJSONStatusCommandClean(t, statusOpts)
}

func assertRecoverJSONDaemonStartupScanClean(t *testing.T, response api.StatusResponse) {
	t.Helper()
	if response.Connection != "inactive" {
		t.Fatalf("expected inactive lifecycle, got %#v", response)
	}
	if response.StartupScan == nil {
		t.Fatal("daemon status did not publish startup recovery scan")
	}
	if response.StartupScan.Status != api.StartupScanStatusClean {
		t.Fatalf("exact exit-0 missing-link observation must publish a clean startup scan, got %#v", response.StartupScan)
	}
	if len(response.StartupScan.Candidates) != 0 || len(response.StartupScan.Warnings) != 0 {
		t.Fatalf("clean startup scan must have no candidates or warnings, got %#v", response.StartupScan)
	}
}

func assertRecoverJSONStatusCommandClean(t *testing.T, opts options) {
	t.Helper()
	var out bytes.Buffer
	err := runStatusCommand(context.Background(), nil, &out, opts)
	if got := ExitCode(err); got != 0 {
		t.Fatalf("status must exit 0 for exact exit-0 missing-link observation: code=%d err=%v output=%q", got, err, out.String())
	}
	if got := out.String(); got != "Status: Disconnected\n" {
		t.Fatalf("clean inactive status must use concise product output, got %q", got)
	}
}

func assertRecoverJSONDryRunClean(t *testing.T, data []byte) {
	t.Helper()
	var payload struct {
		Status   string   `json:"status"`
		Warnings []string `json:"warnings"`
		Recovery struct {
			Candidates []json.RawMessage `json:"candidates"`
			Warnings   []json.RawMessage `json:"warnings"`
		} `json:"recovery"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode recover dry-run JSON: %v; output=%q", err, string(data))
	}
	if payload.Status != "ok" {
		t.Fatalf("clean recover --json must report status ok, got %q", payload.Status)
	}
	if len(payload.Warnings) != 0 || len(payload.Recovery.Candidates) != 0 || len(payload.Recovery.Warnings) != 0 {
		t.Fatalf("clean recover --json must have no warnings or candidates, got %s", string(data))
	}
}

func assertRecoverJSONExecuteClean(t *testing.T, data []byte) {
	t.Helper()
	var payload struct {
		Status   string            `json:"status"`
		Warnings []json.RawMessage `json:"warnings"`
		Errors   []string          `json:"errors"`
		Recovery []json.RawMessage `json:"recovery"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode recover execute JSON: %v; output=%q", err, string(data))
	}
	if payload.Status != "ok" {
		t.Fatalf("clean recover execute JSON must report status ok, got %q", payload.Status)
	}
	if len(payload.Warnings) != 0 || len(payload.Errors) != 0 || len(payload.Recovery) != 0 {
		t.Fatalf("clean recover execute JSON must have no warnings, errors, or cleanup results, got %s", string(data))
	}
}

func writeRecoverJSONFakeCommand(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
}

func recoverJSONResolvedStatusCallCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("read resolved call counter: %v", err)
	}
	return len(strings.Fields(string(data)))
}
