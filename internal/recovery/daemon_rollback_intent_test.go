package recovery

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestDaemonCleanupExecutorUsesDesiredPlanAfterPreApplyCrash(t *testing.T) {
	runtimeDir := t.TempDir()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx := txstate.NewTransaction("tx-pre-apply-crash", "profile-1", "tun", time.Now().UTC())
	tx.State = txstate.TransactionApplying
	tx.DesiredPlan.DNS = txstate.DNSPlan{
		Backend:       planner.DNSBackendSystemdResolved,
		Link:          managedInterface,
		Servers:       []string{planner.DefaultTunDNSServer},
		SearchDomains: []string{"~."},
		Owner:         txstate.TransactionOwner,
	}
	path, err := store.Save(tx)
	if err != nil {
		t.Fatalf("save transaction: %v", err)
	}

	runner := fakeRunner{
		paths: map[string]string{"resolvectl": "/usr/bin/resolvectl"},
		commands: map[string]fakeCommand{
			"resolvectl revert podlaz0": {},
		},
	}
	results := (DaemonCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "dns", "recovered", "")
	assertCleanupResult(t, results, "transaction-state", "recovered", "")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("successful desired-plan recovery must remove transaction state, stat err=%v", err)
	}
}
