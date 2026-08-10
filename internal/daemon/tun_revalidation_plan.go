package daemon

import (
	"fmt"
	"strconv"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// tunRevalidationPlanFromTransaction builds a read-only verification projection
// from durable desired state, not from rollback authority. Rollback metadata is
// still validated first, but it only answers what Podlaz may clean up. Desired
// state also contains exact prerequisites that may have pre-existed connect and
// therefore correctly have no rollback entry.
func tunRevalidationPlanFromTransaction(tx txstate.Transaction) (planner.TunPlan, error) {
	if err := validateTunRollbackProjection(tx); err != nil {
		return planner.TunPlan{}, fmt.Errorf("validate persisted TUN rollback authority: %w", err)
	}
	if tx.Mode != planner.ModeTun {
		return planner.TunPlan{}, fmt.Errorf("persisted transaction mode %q is not TUN", tx.Mode)
	}
	plan := planner.TunPlan{Mode: tx.Mode, ProfileID: tx.ProfileID}

	device, err := tunRevalidationDevicePlan(tx.DesiredPlan.TUN)
	if err != nil {
		return planner.TunPlan{}, err
	}
	plan.TunDevice = device

	address, err := tunRevalidationAddressPlan(tx.DesiredPlan.TUNAddress, device.Name)
	if err != nil {
		return planner.TunPlan{}, err
	}
	plan.TunAddress = address

	routes, serverBypass, err := tunRevalidationRoutePlans(tx)
	if err != nil {
		return planner.TunPlan{}, err
	}
	plan.Routes = routes
	plan.ServerBypass = serverBypass

	policyRules, err := tunRevalidationPolicyRulePlans(tx.DesiredPlan.Steps)
	if err != nil {
		return planner.TunPlan{}, err
	}
	plan.PolicyRules = policyRules

	dns, err := tunRevalidationDNSPlan(tx.DesiredPlan.DNS, device.Name)
	if err != nil {
		return planner.TunPlan{}, err
	}
	plan.DNS = dns

	firewall, err := tunRevalidationFirewallPlan(tx)
	if err != nil {
		return planner.TunPlan{}, err
	}
	plan.Firewall = firewall
	return plan, nil
}

func tunRevalidationDevicePlan(desired txstate.TUNDesiredState) (planner.TunDevicePlan, error) {
	if desired.Owner != xrayTunInboundOwner || strings.TrimSpace(desired.InterfaceName) == "" {
		return planner.TunDevicePlan{}, fmt.Errorf("persisted Xray-owned TUN device identity is incomplete")
	}
	if desired.MTU <= 0 {
		return planner.TunDevicePlan{}, fmt.Errorf("persisted TUN MTU %d is invalid", desired.MTU)
	}
	return planner.TunDevicePlan{Name: desired.InterfaceName, MTU: desired.MTU, Action: "verify"}, nil
}

func tunRevalidationAddressPlan(desired txstate.TUNAddressDesiredState, device string) (planner.TunAddressPlan, error) {
	if strings.TrimSpace(desired.CIDR) == "" || desired.Owner != netexecutor.OwnerTunAddress {
		return planner.TunAddressPlan{}, fmt.Errorf("persisted TUN address desired state is incomplete")
	}
	if desired.InterfaceName != device || desired.Family != "ipv4" || desired.LinkIndex <= 0 || desired.LinkKind != "tun" || !desired.AppearedAfterCore {
		return planner.TunAddressPlan{}, fmt.Errorf("persisted TUN address identity does not match the Xray-created link")
	}
	return planner.TunAddressPlan{
		Family:             desired.Family,
		Interface:          desired.InterfaceName,
		CIDR:               desired.CIDR,
		Scope:              desired.Scope,
		Action:             planner.TunAddressActionAssign,
		Owner:              desired.Owner,
		RollbackKey:        desired.InterfaceName + "/" + desired.CIDR,
		LinkIndex:          desired.LinkIndex,
		LinkKind:           desired.LinkKind,
		AppearedAfterCore:  desired.AppearedAfterCore,
		AllowOwnedExisting: true,
	}, nil
}

func tunRevalidationRoutePlans(tx txstate.Transaction) ([]planner.TunRoutePlan, planner.TunRoutePlan, error) {
	server, err := tunRevalidationServerAddress(tx)
	if err != nil {
		return nil, planner.TunRoutePlan{}, err
	}
	serverCIDR := server + "/32"
	seen := make(map[string]struct{}, len(tx.DesiredPlan.Routes))
	routes := make([]planner.TunRoutePlan, 0, len(tx.DesiredPlan.Routes))
	var serverBypass planner.TunRoutePlan
	for _, desired := range tx.DesiredPlan.Routes {
		if desired.Kind != "route" || desired.Operation != "add" || !rollbackOwnerMatches(desired.Owner, netexecutor.OwnerRoute) {
			return nil, planner.TunRoutePlan{}, fmt.Errorf("persisted desired route is unsupported or incomplete")
		}
		if strings.TrimSpace(desired.Table) == "" || strings.TrimSpace(desired.CIDR) == "" || strings.TrimSpace(desired.Dev) == "" {
			return nil, planner.TunRoutePlan{}, fmt.Errorf("persisted desired route tuple is incomplete")
		}
		key := strings.Join([]string{desired.Table, desired.CIDR, desired.Via, desired.Dev}, "|")
		if _, exists := seen[key]; exists {
			return nil, planner.TunRoutePlan{}, fmt.Errorf("duplicate persisted desired route %s", key)
		}
		seen[key] = struct{}{}
		route := planner.TunRoutePlan{
			Family:      "ipv4",
			Destination: desired.CIDR,
			Table:       desired.Table,
			Interface:   desired.Dev,
			Gateway:     desired.Via,
			Action:      "add",
		}
		routes = append(routes, route)
		if strings.EqualFold(desired.Table, planner.MainRoutingTable) && desired.CIDR == serverCIDR && desired.Dev != deviceNameForRevalidation(tx) {
			if serverBypass.Destination != "" {
				return nil, planner.TunRoutePlan{}, fmt.Errorf("multiple desired server bypass routes match %s", serverCIDR)
			}
			serverBypass = route
		}
	}
	if serverBypass.Destination == "" {
		return nil, planner.TunRoutePlan{}, fmt.Errorf("desired server bypass route %s is missing", serverCIDR)
	}
	return routes, serverBypass, nil
}

func deviceNameForRevalidation(tx txstate.Transaction) string {
	return strings.TrimSpace(tx.DesiredPlan.TUN.InterfaceName)
}

func tunRevalidationPolicyRulePlans(steps []txstate.PlannedStep) ([]planner.TunPolicyRulePlan, error) {
	seen := make(map[string]struct{})
	var rules []planner.TunPolicyRulePlan
	for _, step := range steps {
		if step.Kind != "policy-rule" {
			continue
		}
		if !rollbackOwnerMatches(step.Owner, netexecutor.OwnerPolicyRule) {
			return nil, fmt.Errorf("persisted desired policy rule has unsupported owner")
		}
		rule, err := parsePersistedTunPolicyRule(step.Target)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[step.Target]; exists {
			return nil, fmt.Errorf("duplicate persisted desired policy rule %q", step.Target)
		}
		seen[step.Target] = struct{}{}
		rules = append(rules, rule)
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("persisted desired TUN policy rules are missing")
	}
	return rules, nil
}

func parsePersistedTunPolicyRule(target string) (planner.TunPolicyRulePlan, error) {
	fields := strings.Fields(target)
	if len(fields) < 6 || fields[0] != "priority" || fields[len(fields)-2] != "lookup" {
		return planner.TunPolicyRulePlan{}, fmt.Errorf("persisted policy-rule target %q has unsupported syntax", target)
	}
	priority, err := strconv.Atoi(fields[1])
	if err != nil || priority <= 0 {
		return planner.TunPolicyRulePlan{}, fmt.Errorf("persisted policy-rule target %q has invalid priority", target)
	}
	selector := strings.Join(fields[2:len(fields)-2], " ")
	if !(strings.HasPrefix(selector, "from ") || strings.HasPrefix(selector, "to ")) {
		return planner.TunPolicyRulePlan{}, fmt.Errorf("persisted policy-rule target %q has invalid selector", target)
	}
	table := strings.TrimSpace(fields[len(fields)-1])
	if table == "" {
		return planner.TunPolicyRulePlan{}, fmt.Errorf("persisted policy-rule target %q has no table", target)
	}
	return planner.TunPolicyRulePlan{
		Family:   "ipv4",
		Priority: priority,
		Selector: selector,
		Table:    table,
		Action:   "add",
	}, nil
}

func tunRevalidationDNSPlan(desired txstate.DNSPlan, device string) (planner.TunDNSPlan, error) {
	if !rollbackOwnerMatches(desired.Owner, netexecutor.OwnerDNS) || strings.TrimSpace(desired.Backend) == "" || strings.TrimSpace(desired.Link) == "" || len(desired.Servers) == 0 {
		return planner.TunDNSPlan{}, fmt.Errorf("persisted desired DNS state is incomplete")
	}
	if desired.Link != device {
		return planner.TunDNSPlan{}, fmt.Errorf("persisted desired DNS link %s does not match TUN device %s", desired.Link, device)
	}
	return planner.TunDNSPlan{
		Backend:    desired.Backend,
		TargetLink: desired.Link,
		Servers:    append([]string{}, desired.Servers...),
		Action:     planner.DNSActionConfigure,
	}, nil
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
				return planner.TunFirewallPlan{}, fmt.Errorf("persisted nftables rule-to-chain mapping is ambiguous across %d chains", len(nft.Chains))
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
			rule, err := parsePersistedTunFirewallRule(nft.Family, nft.Table, persisted.Name, rawRule)
			if err != nil {
				return planner.TunFirewallPlan{}, err
			}
			firewall.Rules = append(firewall.Rules, rule)
		}
	}
	return firewall, nil
}

func parsePersistedTunFirewallRule(family, table, chain, raw string) (planner.TunFirewallRulePlan, error) {
	fields := strings.Fields(raw)
	if len(fields) < 4 || fields[len(fields)-2] != "owner" {
		return planner.TunFirewallRulePlan{}, fmt.Errorf("persisted nftables rule on %s has no exact ownership marker", chain)
	}
	ownership := fields[len(fields)-1]
	const ownershipPrefix = "podlaz:firewall:"
	if !strings.HasPrefix(ownership, ownershipPrefix) {
		return planner.TunFirewallRulePlan{}, fmt.Errorf("persisted nftables rule on %s has unsupported owner %q", chain, ownership)
	}
	ownerKey := strings.TrimPrefix(ownership, ownershipPrefix)
	if ownerKey == "" {
		return planner.TunFirewallRulePlan{}, fmt.Errorf("persisted nftables rule on %s has empty ownership key", chain)
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
		RollbackKey: strings.Join([]string{family, table, chain, ownerKey}, "/"),
	}, nil
}
