package recovery

import (
	"context"
	"os"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestDaemonCleanupPreservesUnrecordedExistingMainBypass(t *testing.T) {
	runtimeDir := t.TempDir()
	path, tx := saveDesiredMainBypassTransaction(t, runtimeDir, "tx-main-present")
	runner := fakeRunner{
		paths: map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]fakeCommand{
			"ip -4 route show table main 203.0.113.10/32": {
				stdout: "203.0.113.10 via 192.0.2.1 dev eth0",
			},
			"ip -4 rule show priority 9999": {
				stdout: "9999: from all to 203.0.113.10 lookup main",
			},
		},
	}

	results := (DaemonCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "route", "skipped", "did not durably record")
	assertCleanupResult(t, results, "policy-rule", "skipped", "did not durably record")
	assertCleanupResult(t, results, "transaction-state", "skipped", "transaction state was preserved")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("ambiguous main-table ownership must preserve transaction state: %v", err)
	}
}

func TestDaemonCleanupRemovesTransactionWhenUnrecordedMainBypassIsAbsent(t *testing.T) {
	runtimeDir := t.TempDir()
	path, tx := saveDesiredMainBypassTransaction(t, runtimeDir, "tx-main-absent")
	runner := fakeRunner{
		paths: map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]fakeCommand{
			"ip -4 route show table main 203.0.113.10/32": {},
			"ip -4 rule show priority 9999":                    {},
		},
	}

	results := (DaemonCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "route", "recovered", "")
	assertCleanupResult(t, results, "policy-rule", "recovered", "")
	assertCleanupResult(t, results, "transaction-state", "recovered", "")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("clean absent main-table state must allow transaction removal, stat err=%v", err)
	}
}

func saveDesiredMainBypassTransaction(t *testing.T, runtimeDir, id string) (string, txstate.Transaction) {
	t.Helper()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx := txstate.NewTransaction(id, "profile-1", planner.ModeTun, time.Now().UTC())
	tx.State = txstate.TransactionApplying
	tx.DesiredPlan.Routes = []txstate.RoutePlan{{
		Table:     planner.MainRoutingTable,
		CIDR:      "203.0.113.10/32",
		Via:       "192.0.2.1",
		Dev:       "eth0",
		Owner:     netexecutor.OwnerRoute,
		Operation: "add",
	}}
	tx.DesiredPlan.Steps = []txstate.PlannedStep{{
		Kind:   "policy-rule",
		Target: "priority 9999 to 203.0.113.10/32 lookup main",
		Owner:  netexecutor.OwnerPolicyRule,
	}}
	path, err := store.Save(tx)
	if err != nil {
		t.Fatalf("save transaction: %v", err)
	}
	return path, tx
}
