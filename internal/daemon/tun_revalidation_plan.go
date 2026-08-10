package daemon

import (
	"fmt"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func tunRevalidationPlanFromTransaction(tx txstate.Transaction) (planner.TunPlan, error) {
	plan := tunPlanFromTransaction(tx)
	if plan.TunDevice.Action == "invalid-rollback-projection" {
		return planner.TunPlan{}, fmt.Errorf("restore persisted TUN verification plan: %s", plan.TunDevice.Reason)
	}
	plan.Mode = tx.Mode
	plan.ProfileID = tx.ProfileID

	if strings.TrimSpace(plan.TunDevice.Name) == "" {
		if tx.DesiredPlan.TUN.Owner != xrayTunInboundOwner || strings.TrimSpace(tx.DesiredPlan.TUN.InterfaceName) == "" {
			return planner.TunPlan{}, fmt.Errorf("persisted TUN device ownership is incomplete")
		}
		plan.TunDevice = planner.TunDevicePlan{
			Name:   tx.DesiredPlan.TUN.InterfaceName,
			MTU:    tx.DesiredPlan.TUN.MTU,
			Action: "verify",
		}
	}

	if len(tx.Rollback.NFTables) > 0 {
		firewall, err := tunRevalidationFirewallPlan(tx)
		if err != nil {
			return planner.TunPlan{}, err
		}
		plan.Firewall = firewall
	}
	return plan, nil
}

func tunRevalidationFirewallPlan(tx txstate.Transaction) (planner.TunFirewallPlan, error) {
	if len(tx.Rollback.NFTables) != 1 {
		return planner.TunFirewallPlan{}, fmt.Errorf("persisted nftables rollback cardinality=%d, want 1", len(tx.Rollback.NFTables))
	}
	rollback := tx.Rollback.NFTables[0]
	if !rollbackOwnerMatches(rollback.Owner, netexecutor.OwnerFirewall) {
		return planner.TunFirewallPlan{}, fmt.Errorf("persisted nftables rollback owner is unsupported")
	}
	nft := tx.DesiredPlan.NFT
	if nft.Owner != netexecutor.OwnerFirewall || nft.Family != rollback.Family || nft.Table != rollback.Table {
		return planner.TunFirewallPlan{}, fmt.Errorf("persisted nftables desired state does not match rollback authority")
	}
	if len(nft.Chains) == 0 {
		return planner.TunFirewallPlan{}, fmt.Errorf("persisted nftables desired state has no chains")
	}
	if len(nft.Chains) > 1 {
		for _, chain := range nft.Chains {
			if len(chain.Rules) > 0 {
				return planner.TunFirewallPlan{}, fmt.Errorf("persisted nftables rule-to-chain ownership is ambiguous across %d chains", len(nft.Chains))
			}
		}
	}

	firewall := planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      nft.Family,
		Table:       nft.Table,
		TableAction: planner.FirewallTableAction,
	}
	for _, persisted := range nft.Chains {
		if persisted.Owner != "" && !rollbackOwnerMatches(persisted.Owner, netexecutor.OwnerFirewall) {
			return planner.TunFirewallPlan{}, fmt.Errorf("persisted nftables chain %s has unsupported owner", persisted.Name)
		}
		chain := planner.TunFirewallChainPlan{
			Name:     persisted.Name,
			Type:     persisted.Type,
			Hook:     persisted.Hook,
			Priority: persisted.Priority,
			Policy:   persisted.Policy,
			Action:   planner.FirewallTableAction,
		}
		firewall.Chains = append(firewall.Chains, chain)
		for _, rawRule := range persisted.Rules {
			rule, err := parsePersistedTunFirewallRule(persisted.Name, rawRule)
			if err != nil {
				return planner.TunFirewallPlan{}, err
			}
			firewall.Rules = append(firewall.Rules, rule)
		}
	}
	return firewall, nil
}

func parsePersistedTunFirewallRule(chain, raw string) (planner.TunFirewallRulePlan, error) {
	fields := strings.Fields(raw)
	if len(fields) < 4 || fields[len(fields)-2] != "owner" {
		return planner.TunFirewallRulePlan{}, fmt.Errorf("persisted nftables rule on %s has no exact ownership marker", chain)
	}
	ownership := fields[len(fields)-1]
	if !strings.HasPrefix(ownership, "podlaz:firewall:") {
		return planner.TunFirewallRulePlan{}, fmt.Errorf("persisted nftables rule on %s has unsupported owner %q", chain, ownership)
	}
	verdictIndex := len(fields) - 3
	if verdictIndex <= 0 {
		return planner.TunFirewallRulePlan{}, fmt.Errorf("persisted nftables rule on %s has no expression", chain)
	}
	verdict := fields[verdictIndex]
	switch verdict {
	case planner.FirewallVerdictAccept, planner.FirewallVerdictReject, planner.FirewallVerdictDrop:
	default:
		return planner.TunFirewallRulePlan{}, fmt.Errorf("persisted nftables rule on %s has unsupported verdict %q", chain, verdict)
	}
	expr := strings.Join(fields[:verdictIndex], " ")
	return planner.TunFirewallRulePlan{
		Chain:       chain,
		Expr:        expr,
		Verdict:     verdict,
		Action:      planner.FirewallActionAdd,
		Ownership:   ownership,
		RollbackKey: strings.Join([]string{"inet", "podlaz", chain, ownership}, "/"),
	}, nil
}
