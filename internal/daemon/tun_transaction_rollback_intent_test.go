package daemon

import (
	"context"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestBeginTunTransactionPersistsRecoveryIntentBeforeMutation(t *testing.T) {
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
	if tx.DesiredPlan.TUN.InterfaceName != "podlaz0" || len(tx.DesiredPlan.Routes) != 1 || tx.DesiredPlan.DNS.Link != "podlaz0" || tx.DesiredPlan.NFT.Table != "podlaz" {
		t.Fatalf("expected structured desired plan to provide durable recovery intent, got %#v", tx.DesiredPlan)
	}
	foundPolicyRule := false
	for _, step := range tx.DesiredPlan.Steps {
		if step.Kind == "policy-rule" && step.Owner != "" {
			foundPolicyRule = true
			break
		}
	}
	if !foundPolicyRule {
		t.Fatalf("expected structured policy-rule recovery intent, got %#v", tx.DesiredPlan.Steps)
	}
	if tx.Owner != txstate.TransactionOwner {
		t.Fatalf("unexpected transaction owner: %q", tx.Owner)
	}
}
