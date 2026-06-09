package daemon

import (
	"testing"

	netexecutor "github.com/AidarKhusainov/tunwarden/internal/network/executor"
	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
)

func TestTunTransactionMetadataIncludesFirewallRollback(t *testing.T) {
	plan := transactionPlanForTest()
	plan.Firewall = planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      "inet",
		Table:       "tunwarden",
		TableAction: planner.FirewallTableAction,
		Chains: []planner.TunFirewallChainPlan{{
			Name:     planner.FirewallOutputChain,
			Type:     planner.FirewallChainTypeFilter,
			Hook:     planner.FirewallOutputHook,
			Priority: planner.FirewallOutputPriority,
			Policy:   planner.FirewallDefaultChainPolicy,
			Action:   planner.FirewallTableAction,
		}},
		Rules: []planner.TunFirewallRulePlan{{
			Chain:       planner.FirewallOutputChain,
			Expr:        "oifname != \"tunwarden0\"",
			Verdict:     planner.FirewallVerdictReject,
			Action:      planner.FirewallActionAdd,
			Ownership:   planner.FirewallKillSwitchOwner,
			RollbackKey: planner.FirewallKillSwitchKey,
		}},
		KillSwitch: planner.TunKillSwitchPlan{Policy: planner.KillSwitchPolicySoft},
		Reason:     "create a TunWarden-owned nftables table",
		Rollback:   planner.FirewallRollbackRemove,
	}

	desired := desiredPlanFromTunPlan(plan)
	if desired.NFT.Family != "inet" || desired.NFT.Table != "tunwarden" || desired.NFT.Owner != netexecutor.OwnerFirewall {
		t.Fatalf("expected nftables desired state, got %#v", desired.NFT)
	}
	if len(desired.NFT.Chains) != 1 || desired.NFT.Chains[0].Name != planner.FirewallOutputChain || len(desired.NFT.Chains[0].Rules) != 1 {
		t.Fatalf("expected nftables chain and rule desired state, got %#v", desired.NFT.Chains)
	}

	rollback := rollbackMetadataFromTunPlan(plan)
	if len(rollback.NFTables) != 1 || rollback.NFTables[0].Family != "inet" || rollback.NFTables[0].Table != "tunwarden" || rollback.NFTables[0].Owner != netexecutor.OwnerFirewall {
		t.Fatalf("expected nftables rollback metadata, got %#v", rollback.NFTables)
	}

	partial := rollbackPlanFromAppliedSteps(plan, []netexecutor.Step{{Kind: "nftables", Target: "inet tunwarden", Owner: netexecutor.OwnerFirewall}})
	if partial.Firewall.Family != "inet" || partial.Firewall.Table != "tunwarden" {
		t.Fatalf("expected partial rollback plan to include applied firewall state, got %#v", partial.Firewall)
	}
}
