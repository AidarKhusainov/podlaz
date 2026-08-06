package recovery

import (
	"context"
	"os"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestDaemonCleanupExecutorRemovesOwnedMainTableServerBypassPolicyRule(t *testing.T) {
	runtimeDir := t.TempDir()
	runner := &recordingRunner{
		paths: map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]fakeCommand{
			"ip -4 rule del priority 9999 to 203.0.113.10/32 lookup main": {},
		},
	}
	path, tx := saveTransaction(t, runtimeDir, txstate.RollbackMetadata{
		PolicyRules: []txstate.PolicyRuleRollback{{
			Owner:    netexecutor.OwnerPolicyRule,
			Priority: planner.ServerRulePriority,
			To:       "203.0.113.10/32",
			Table:    planner.MainRoutingTable,
		}},
	})

	results := (DaemonCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "policy-rule", "recovered", "")
	assertCleanupResult(t, results, "transaction-state", "recovered", "")
	assertCommands(t, runner, []string{"ip -4 rule del priority 9999 to 203.0.113.10/32 lookup main"})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("transaction file must be removed after complete cleanup, stat err=%v", err)
	}
}

func TestDaemonCleanupExecutorRejectsMainTablePolicyRuleWithWrongPriority(t *testing.T) {
	assertMainTablePolicyRuleSkipped(t, txstate.PolicyRuleRollback{
		Owner:    netexecutor.OwnerPolicyRule,
		Priority: planner.TunRulePriority,
		To:       "203.0.113.10/32",
		Table:    planner.MainRoutingTable,
	})
}

func TestDaemonCleanupExecutorRejectsMainTablePolicyRuleWithNonHostTo(t *testing.T) {
	assertMainTablePolicyRuleSkipped(t, txstate.PolicyRuleRollback{
		Owner:    netexecutor.OwnerPolicyRule,
		Priority: planner.ServerRulePriority,
		To:       "203.0.113.0/24",
		Table:    planner.MainRoutingTable,
	})
}

func TestDaemonCleanupExecutorRejectsMainTablePolicyRuleWithFromSelector(t *testing.T) {
	assertMainTablePolicyRuleSkipped(t, txstate.PolicyRuleRollback{
		Owner:    netexecutor.OwnerPolicyRule,
		Priority: planner.ServerRulePriority,
		From:     "0.0.0.0/0",
		To:       "203.0.113.10/32",
		Table:    planner.MainRoutingTable,
	})
}

func TestDaemonCleanupExecutorRejectsMainTablePolicyRuleWithMark(t *testing.T) {
	assertMainTablePolicyRuleSkipped(t, txstate.PolicyRuleRollback{
		Owner:    netexecutor.OwnerPolicyRule,
		Priority: planner.ServerRulePriority,
		To:       "203.0.113.10/32",
		Table:    planner.MainRoutingTable,
		Mark:     "0x1",
	})
}

func TestDaemonCleanupExecutorRejectsNonPodlazPolicyRuleOwner(t *testing.T) {
	assertMainTablePolicyRuleSkipped(t, txstate.PolicyRuleRollback{
		Owner:    "other",
		Priority: planner.ServerRulePriority,
		To:       "203.0.113.10/32",
		Table:    planner.MainRoutingTable,
	})
}

func assertMainTablePolicyRuleSkipped(t *testing.T, rule txstate.PolicyRuleRollback) {
	t.Helper()
	runtimeDir := t.TempDir()
	runner := &recordingRunner{paths: map[string]string{"ip": "/usr/sbin/ip"}}
	path, tx := saveInvalidRawTransaction(t, runtimeDir, txstate.RollbackMetadata{PolicyRules: []txstate.PolicyRuleRollback{rule}})

	results := (DaemonCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "transaction-state", "skipped", "transaction state was preserved")
	assertCommands(t, runner, nil)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("invalid transaction must remain: %v", err)
	}
}
