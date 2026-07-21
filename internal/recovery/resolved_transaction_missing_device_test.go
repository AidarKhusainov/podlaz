package recovery

import (
	"context"
	"fmt"
	"os"
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
	runner := fakeRunner{
		paths: map[string]string{
			"resolvectl": "/usr/bin/resolvectl",
		},
		commands: map[string]fakeCommand{
			"resolvectl revert podlaz0": {
				stderr:   resolvedMissingDeviceStderr,
				exitCode: 1,
				err:      resolvedTestExitError(1),
			},
		},
	}

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

type resolvedTestExitError int

func (e resolvedTestExitError) Error() string {
	return fmt.Sprintf("exit status %d", int(e))
}

func (e resolvedTestExitError) ExitCode() int {
	return int(e)
}
