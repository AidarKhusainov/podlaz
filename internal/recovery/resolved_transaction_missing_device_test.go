package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestDaemonCleanupExecutorTreatsResolvedNoSuchDeviceAsSuccessfulDNSRollback(t *testing.T) {
	runtimeDir := t.TempDir()
	path, tx := saveTransaction(t, runtimeDir, txstate.RollbackMetadata{
		DNS: []txstate.DNSRollback{{
			Link:    managedInterface,
			Backend: "systemd-resolved",
			Owner:   netexecutor.OwnerDNS,
		}},
	})
	runner := rawResolvedTransactionRunner{}

	results := (DaemonCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "dns", "recovered", "")
	assertCleanupResult(t, results, "transaction-state", "recovered", "")
	for _, result := range results {
		if result.Status == "failed" {
			t.Fatalf("resolved missing-device rollback must not fail transaction recovery: %#v", results)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("successful transaction recovery must remove transaction state, stat err=%v", err)
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
