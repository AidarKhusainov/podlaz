package daemon

import (
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTunRevalidationPlanRestoresExactOwnedFirewallRules(t *testing.T) {
	tx := txstate.Transaction{
		Mode:      planner.ModeTun,
		ProfileID: "profile-test",
		DesiredPlan: txstate.DesiredPlan{
			TUN: txstate.TUNDesiredState{InterfaceName: "podlaz0", MTU: 1500, Owner: xrayTunInboundOwner},
			NFT: txstate.NFTPlan{
				Family: "inet",
				Table:  "podlaz",
				Owner:  netexecutor.OwnerFirewall,
				Chains: []txstate.NFTChainPlan{{
					Name: "output", Type: "filter", Hook: "output", Priority: 0, Policy: "accept", Owner: netexecutor.OwnerFirewall,
					Rules: []string{"oifname podlaz0 accept owner podlaz:firewall:tun-egress"},
				}},
		},
		Rollback: txstate.RollbackMetadata{
			NFTables: []txstate.NFTablesRollback{{Family: "inet", Table: "podlaz", Owner: netexecutor.OwnerFirewall}},
		},
	}

	plan, err := tunRevalidationPlanFromTransaction(tx)
	if err != nil {
		t.Fatalf("restore revalidation plan: %v", err)
	}
	if plan.TunDevice.Action != "verify" || plan.TunDevice.Name != "podlaz0" {
		t.Fatalf("unexpected TUN device plan: %#v", plan.TunDevice)
	}
	if len(plan.Firewall.Chains) != 1 || len(plan.Firewall.Rules) != 1 {
		t.Fatalf("unexpected firewall plan: %#v", plan.Firewall)
	}
	rule := plan.Firewall.Rules[0]
	if rule.Chain != "output" || rule.Expr != "oifname podlaz0" || rule.Verdict != "accept" || rule.Ownership != "podlaz:firewall:tun-egress" {
		t.Fatalf("firewall rule was not restored exactly: %#v", rule)
	}
}

func TestTunRevalidationPlanRejectsFirewallRuleWithoutOwnershipMarker(t *testing.T) {
	tx := txstate.Transaction{
		Mode: planner.ModeTun,
		DesiredPlan: txstate.DesiredPlan{NFT: txstate.NFTPlan{
			Family: "inet", Table: "podlaz", Owner: netexecutor.OwnerFirewall,
			Chains: []txstate.NFTChainPlan{{Name: "output", Type: "filter", Hook: "output", Policy: "accept", Rules: []string{"oifname podlaz0 accept"}}},
		}},
		Rollback: txstate.RollbackMetadata{NFTables: []txstate.NFTablesRollback{{Family: "inet", Table: "podlaz", Owner: netexecutor.OwnerFirewall}}},
	}
	if _, err := tunRevalidationPlanFromTransaction(tx); err == nil {
		t.Fatal("expected malformed persisted firewall rule to fail closed")
	}
}
