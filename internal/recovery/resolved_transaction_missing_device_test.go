package recovery

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestResolvedRollbackTreatsExactMissingDeviceAsSuccess(t *testing.T) {
	runner := rawResolvedTransactionRunner{}
	osExec := OSCleanupExecutor{Runner: runner}
	results := (DaemonCleanupExecutor{}).rollbackDNSResults(context.Background(), osExec, []txstate.DNSRollback{{
		Link:    managedInterface,
		Backend: "systemd-resolved",
		Owner:   netexecutor.OwnerDNS,
	}})

	assertCleanupResult(t, results, "dns", "recovered", "")
	for _, result := range results {
		if result.Status == "failed" {
			t.Fatalf("resolved missing-device DNS rollback must converge: %#v", results)
		}
	}
}

type rawResolvedTransactionRunner struct{}

func (rawResolvedTransactionRunner) LookPath(file string) (string, error) {
	if file == "resolvectl" {
		return "/usr/bin/resolvectl", nil
	}
	return "", errors.New("command not found")
}

func (rawResolvedTransactionRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	if filepath.Base(name) != "resolvectl" || strings.Join(args, " ") != "revert podlaz0" {
		return CommandResult{ExitCode: -1}, errors.New("unexpected command")
	}
	rawStderr := resolvedMissingDeviceStderr + "\n"
	return CommandResult{
		Stderr:    strings.TrimSpace(rawStderr),
		RawStderr: rawStderr,
		ExitCode:  1,
	}, resolvedTestExitError{code: 1}
}
