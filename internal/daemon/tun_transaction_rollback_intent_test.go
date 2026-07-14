package daemon

import (
	"context"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestBeginTunTransactionPersistsRollbackIntentBeforeMutation(t *testing.T) {
	plan := transactionPlanForTest()
	plan.DNS = planner.TunDNSPlan{
		Backend:    planner.DNSBackendSystemdResolved,
		TargetLink: "podlaz0",
		Servers:    []string{planner.DefaultTunDNSServer},
		Action:     planner.DNSActionConfigure,
	}
	plan.Firewall = planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      "inet",
		Table:       "podlaz",
		TableAction: planner.FirewallTableAction,
	}

	result, err := beginTunTransaction(context.Background(), t.TempDir(), profile.Profile{ID: "test-profile"}, plan, fixedClock())
	if err != nil {
		t.Fatalf("begin TUN transaction: %v", err)
	}
	tx, _, err := result.Store.Load(result.TransactionID)
	if err != nil {
		t.Fatalf("load transaction: %v", err)
	}

	if tx.Rollback.Available() {
		t.Fatalf("pre-apply transaction must not claim applied ownership: %#v", tx.Rollback)
	}
	intent := tx.RollbackIntent
	if len(intent.TUN) != 1 || len(intent.Routes) != 1 || len(intent.PolicyRules) != 1 || len(intent.DNS) != 1 || len(intent.NFTables) != 1 {
		t.Fatalf("expected durable rollback intent for every planned host mutation, got %#v", intent)
	}
	if !tx.Summary(result.TransactionPath).RollbackAvailable {
		t.Fatal("transaction summary must expose pre-apply rollback intent as recoverable")
	}
	if tx.Owner != txstate.TransactionOwner {
		t.Fatalf("unexpected transaction owner: %q", tx.Owner)
	}
}
