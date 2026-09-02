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

func TestNetworkSessionCleanupExecutorRecoversExactDynamicRoutingAllocation(t *testing.T) {
	runtimeDir := t.TempDir()
	runner := &recordingRunner{
		paths:    map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]fakeCommand{},
	}
	runner.commands["ip -4 rule del priority 98 to 203.0.113.10/32 lookup main"] = fakeCommand{}
	runner.commands["ip -4 rule del priority 99 from all lookup 51821"] = fakeCommand{}
	runner.commands["ip -4 route del default dev podlaz0 table 51821"] = fakeCommand{}
	runner.commands["ip -4 route del 203.0.113.10/32 dev wg0 table main"] = fakeCommand{}

	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	now := time.Unix(1_700_000_000, 0).UTC()
	tx := txstate.NewTransaction("dynamic-routing", "profile-1", planner.ModeTun, now)
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.TUNAddress = txstate.TUNAddressDesiredState{
		Family:        "ipv4",
		InterfaceName: managedInterface,
		CIDR:          "198.18.0.2/32",
		Scope:         "global",
		Owner:         netexecutor.OwnerTunAddress,
	}
	tx.DesiredPlan.Routes = []txstate.RoutePlan{
		{Kind: "route", Table: "51821", CIDR: planner.IPv4DefaultRoute, Dev: managedInterface, Owner: netexecutor.OwnerRoute, Operation: "add"},
		{Kind: "route", Table: planner.MainRoutingTable, CIDR: "203.0.113.10/32", Dev: "wg0", Owner: netexecutor.OwnerRoute, Operation: "add"},
	}
	tx.DesiredPlan.Steps = []txstate.PlannedStep{
		{Kind: "policy-rule", Target: "priority 98 to 203.0.113.10/32 lookup main", Owner: netexecutor.OwnerPolicyRule},
		{Kind: "policy-rule", Target: "priority 99 from all lookup 51821", Owner: netexecutor.OwnerPolicyRule},
	}
	tx.Rollback.Routes = []txstate.RouteRollback{
		{Table: "51821", CIDR: planner.IPv4DefaultRoute, Dev: managedInterface, Owner: netexecutor.OwnerRoute},
		{Table: planner.MainRoutingTable, CIDR: "203.0.113.10/32", Dev: "wg0", Owner: netexecutor.OwnerRoute},
	}
	tx.Rollback.PolicyRules = []txstate.PolicyRuleRollback{
		{Priority: 98, To: "203.0.113.10/32", Table: planner.MainRoutingTable, Owner: netexecutor.OwnerPolicyRule},
		{Priority: 99, From: "all", Table: "51821", Owner: netexecutor.OwnerPolicyRule},
	}
	for _, route := range tx.Rollback.Routes {
		tx.AppliedSteps = append(tx.AppliedSteps, txstate.AppliedStep{Kind: "route", Target: routeRollbackTarget(route), Owner: netexecutor.OwnerRoute, AppliedAt: now})
	}
	for _, rule := range tx.Rollback.PolicyRules {
		tx.AppliedSteps = append(tx.AppliedSteps, txstate.AppliedStep{Kind: "policy-rule", Target: policyRuleRollbackTarget(rule), Owner: netexecutor.OwnerPolicyRule, AppliedAt: now})
	}
	path, err := store.Save(tx)
	if err != nil {
		t.Fatalf("save dynamic transaction: %v", err)
	}

	results := (NetworkSessionCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "policy-rule", "recovered", "")
	assertCleanupResult(t, results, "route", "recovered", "")
	assertCleanupResult(t, results, "transaction-state", "recovered", "")
	assertCommands(t, runner, []string{
		"ip -4 rule del priority 98 to 203.0.113.10/32 lookup main",
		"ip -4 rule del priority 99 from all lookup 51821",
		"ip -4 route del default dev podlaz0 table 51821",
		"ip -4 route del 203.0.113.10/32 dev wg0 table main",
	})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("transaction file must be removed after exact cleanup, stat err=%v", err)
	}
}

func TestNetworkSessionCleanupExecutorDoesNotAuthorizeDynamicTupleWithoutAppliedProof(t *testing.T) {
	runtimeDir := t.TempDir()
	runner := &recordingRunner{paths: map[string]string{"ip": "/usr/sbin/ip"}}
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	now := time.Unix(1_700_000_000, 0).UTC()
	tx := txstate.NewTransaction("dynamic-routing-no-applied-proof", "profile-1", planner.ModeTun, now)
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.TUNAddress = txstate.TUNAddressDesiredState{Family: "ipv4", InterfaceName: managedInterface, CIDR: "198.18.0.2/32", Scope: "global", Owner: netexecutor.OwnerTunAddress}
	tx.DesiredPlan.Routes = []txstate.RoutePlan{{Kind: "route", Table: "51821", CIDR: planner.IPv4DefaultRoute, Dev: managedInterface, Owner: netexecutor.OwnerRoute, Operation: "add"}}
	tx.DesiredPlan.Steps = []txstate.PlannedStep{
		{Kind: "policy-rule", Target: "priority 98 to 203.0.113.10/32 lookup main", Owner: netexecutor.OwnerPolicyRule},
		{Kind: "policy-rule", Target: "priority 99 from all lookup 51821", Owner: netexecutor.OwnerPolicyRule},
	}
	tx.Rollback.Routes = []txstate.RouteRollback{{Table: "51821", CIDR: planner.IPv4DefaultRoute, Dev: managedInterface, Owner: netexecutor.OwnerRoute}}
	path, err := store.Save(tx)
	if err != nil {
		t.Fatal(err)
	}

	results := (NetworkSessionCleanupExecutor{RuntimeDir: runtimeDir, Runner: runner}).CleanupMany(context.Background(), transactionCandidate(path, tx))
	assertCommands(t, runner, nil)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("transaction without applied proof must be preserved: %v", err)
	}
	if !hasSkippedCleanup(results) && !hasFailedCleanup(results) {
		t.Fatalf("expected fail-closed cleanup result, got %#v", results)
	}
}
