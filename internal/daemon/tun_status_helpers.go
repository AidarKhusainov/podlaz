package daemon

import (
	"fmt"
	"strings"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

const dnsRouteOnlyDomain = "~."

func tunPlanFromTransaction(tx txstate.Transaction) planner.TunPlan {
	plan := planner.TunPlan{Mode: tx.Mode, ProfileID: tx.ProfileID}
	if err := validateTunRollbackProjection(tx); err != nil {
		plan.TunDevice = planner.TunDevicePlan{Name: "podlaz0", Action: "invalid-rollback-projection", Reason: err.Error()}
		return plan
	}
	rollback := tx.Rollback
	if len(rollback.TUN) > 0 {
		for _, tun := range rollback.TUN {
			if !rollbackOwnerMatches(tun.Owner, netexecutor.OwnerTunDevice) {
				continue
			}
			plan.TunDevice = planner.TunDevicePlan{Name: tun.InterfaceName, MTU: tx.DesiredPlan.TUN.MTU, Action: "add"}
			if description := appliedStepDescription(tx.AppliedSteps, "tun-device", tun.InterfaceName, netexecutor.OwnerTunDevice); description != "" {
				plan.TunDevice.Reason = description
			}
			break
		}
	}
	if len(rollback.TUNAddresses) > 0 {
		for _, address := range rollback.TUNAddresses {
			if !rollbackOwnerMatches(address.Owner, netexecutor.OwnerTunAddress) {
				continue
			}
			plan.TunAddress = planner.TunAddressPlan{
				Family:             address.Family,
				Interface:          address.InterfaceName,
				CIDR:               address.CIDR,
				Scope:              address.Scope,
				Action:             planner.TunAddressActionAssign,
				Owner:              address.Owner,
				RollbackKey:        address.InterfaceName + "/" + address.CIDR,
				LinkIndex:          address.LinkIndex,
				LinkKind:           address.LinkKind,
				AppearedAfterCore:  address.AppearedAfterCore,
				AllowOwnedExisting: true,
			}
			break
		}
	}
	for _, route := range rollback.Routes {
		if !rollbackOwnerMatches(route.Owner, netexecutor.OwnerRoute) {
			continue
		}
		plan.Routes = append(plan.Routes, planner.TunRoutePlan{
			Family:      "ipv4",
			Destination: route.CIDR,
			Table:       route.Table,
			Gateway:     route.Via,
			Interface:   route.Dev,
			Action:      "add",
		})
	}
	for _, rule := range rollback.PolicyRules {
		if !rollbackOwnerMatches(rule.Owner, netexecutor.OwnerPolicyRule) {
			continue
		}
		selector := strings.TrimSpace(rule.From)
		if rule.To != "" {
			selector = "to " + rule.To
		} else if selector != "" && !strings.HasPrefix(selector, "from ") {
			selector = "from " + selector
		}
		plan.PolicyRules = append(plan.PolicyRules, planner.TunPolicyRulePlan{
			Family:   "ipv4",
			Priority: rule.Priority,
			Selector: selector,
			Table:    rule.Table,
			Action:   "add",
		})
	}
	if len(rollback.DNS) > 0 {
		for _, dns := range rollback.DNS {
			if !rollbackOwnerMatches(dns.Owner, netexecutor.OwnerDNS) {
				continue
			}
			plan.DNS = planner.TunDNSPlan{
				Backend:    dns.Backend,
				TargetLink: dns.Link,
				Servers:    append([]string{}, tx.DesiredPlan.DNS.Servers...),
				Action:     planner.DNSActionConfigure,
			}
			break
		}
	}
	if len(rollback.NFTables) > 0 {
		for _, nft := range rollback.NFTables {
			if !rollbackOwnerMatches(nft.Owner, netexecutor.OwnerFirewall) {
				continue
			}
			plan.Firewall = planner.TunFirewallPlan{
				Backend:     planner.FirewallBackendNftables,
				Family:      nft.Family,
				Table:       nft.Table,
				TableAction: planner.FirewallTableAction,
			}
			break
		}
	}
	return plan
}

func validateTunRollbackProjection(tx txstate.Transaction) error {
	var reasons []string
	if err := validateSingleRollbackCategory("TUN", len(tx.Rollback.TUN), func(i int) bool {
		entry := tx.Rollback.TUN[i]
		return rollbackOwnerMatches(entry.Owner, netexecutor.OwnerTunDevice) && strings.TrimSpace(entry.InterfaceName) != ""
	}); err != nil {
		reasons = append(reasons, err.Error())
	}
	if err := validateSingleRollbackCategory("TUN address", len(tx.Rollback.TUNAddresses), func(i int) bool {
		entry := tx.Rollback.TUNAddresses[i]
		return rollbackOwnerMatches(entry.Owner, netexecutor.OwnerTunAddress) &&
			entry.InterfaceName == "podlaz0" &&
			strings.TrimSpace(entry.Family) == "ipv4" &&
			strings.TrimSpace(entry.Scope) == "global" &&
			strings.TrimSpace(entry.CIDR) == planner.DefaultTunIPv4CIDR &&
			entry.LinkIndex > 0 && entry.LinkKind == "tun" && entry.AppearedAfterCore
	}); err != nil {
		reasons = append(reasons, err.Error())
	}
	if err := validateSingleRollbackCategory("DNS", len(tx.Rollback.DNS), func(i int) bool {
		entry := tx.Rollback.DNS[i]
		return rollbackOwnerMatches(entry.Owner, netexecutor.OwnerDNS) && strings.TrimSpace(entry.Link) != ""
	}); err != nil {
		reasons = append(reasons, err.Error())
	}
	if err := validateSingleRollbackCategory("nftables", len(tx.Rollback.NFTables), func(i int) bool {
		entry := tx.Rollback.NFTables[i]
		return rollbackOwnerMatches(entry.Owner, netexecutor.OwnerFirewall) && strings.TrimSpace(entry.Family) != "" && strings.TrimSpace(entry.Table) != ""
	}); err != nil {
		reasons = append(reasons, err.Error())
	}
	if len(reasons) > 0 {
		return fmt.Errorf("ambiguous TUN rollback projection: %s", strings.Join(reasons, "; "))
	}
	return nil
}

func validateSingleRollbackCategory(name string, count int, valid func(int) bool) error {
	if count == 0 {
		return nil
	}
	if count != 1 {
		return fmt.Errorf("%s rollback cardinality=%d, want 1", name, count)
	}
	if !valid(0) {
		return fmt.Errorf("%s rollback entry is unsupported or incomplete", name)
	}
	return nil
}

func appliedStepDescription(applied []txstate.AppliedStep, kind, target, owner string) string {
	for _, step := range applied {
		if step.Kind == kind && step.Target == target && rollbackOwnerMatches(step.Owner, owner) {
			return step.Description
		}
	}
	return ""
}

func rollbackOwnerMatches(owner, expected string) bool {
	owner = strings.TrimSpace(owner)
	return owner == expected || owner == txstate.TransactionOwner
}

func routeTarget(route planner.TunRoutePlan) string {
	return route.Table + " " + route.Destination
}

func policyRuleTarget(rule planner.TunPolicyRulePlan) string {
	return fmt.Sprintf("priority %d %s lookup %s", rule.Priority, rule.Selector, rule.Table)
}

func appliedRoutes(plan planner.TunPlan) []planner.TunRoutePlan {
	out := make([]planner.TunRoutePlan, 0, len(plan.Routes))
	for _, route := range plan.Routes {
		if route.Action == "add" {
			out = append(out, route)
		}
	}
	return out
}

func appliedPolicyRules(plan planner.TunPlan) []planner.TunPolicyRulePlan {
	out := make([]planner.TunPolicyRulePlan, 0, len(plan.PolicyRules))
	for _, rule := range plan.PolicyRules {
		if rule.Action == "add" {
			out = append(out, rule)
		}
	}
	return out
}

func dnsStatusLine(plan planner.TunDNSPlan) string {
	if plan.Action == planner.DNSActionConfigure && plan.TargetLink != "" {
		return fmt.Sprintf("%s; Link: %s; Servers: %s; Rollback: available", plan.Backend, plan.TargetLink, strings.Join(plan.Servers, ", "))
	}
	if plan.Action == planner.DNSActionBlocked {
		return "blocked: " + plan.Reason
	}
	return "not modified"
}

func firewallStatusLine(plan planner.TunFirewallPlan) string {
	if plan.TableAction == planner.FirewallTableAction && plan.Table != "" {
		return fmt.Sprintf("%s; Table: %s %s; Kill-switch: %s; Rollback: %s", plan.Backend, plan.Family, plan.Table, plan.KillSwitch.Policy, plan.Rollback)
	}
	if plan.TableAction == planner.FirewallActionBlocked {
		return "blocked: " + plan.Reason
	}
	if plan.TableAction == planner.FirewallActionValidate {
		return fmt.Sprintf("%s; Table: %s %s requires ownership validation before apply", plan.Backend, plan.Family, plan.Table)
	}
	return "not modified"
}

func nftChains(plan planner.TunFirewallPlan) []txstate.NFTChainPlan {
	chains := make([]txstate.NFTChainPlan, 0, len(plan.Chains))
	rules := firewallRuleStrings(plan.Rules)
	for _, chain := range plan.Chains {
		chains = append(chains, txstate.NFTChainPlan{
			Name:     chain.Name,
			Hook:     chain.Hook,
			Type:     chain.Type,
			Priority: chain.Priority,
			Policy:   chain.Policy,
			Rules:    append([]string{}, rules...),
			Owner:    netexecutor.OwnerFirewall,
		})
	}
	return chains
}

func firewallRuleStrings(rules []planner.TunFirewallRulePlan) []string {
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		if rule.Action != planner.FirewallActionAdd {
			continue
		}
		out = append(out, strings.TrimSpace(rule.Expr+" "+rule.Verdict+" owner "+rule.Ownership))
	}
	return out
}

func firewallTarget(plan planner.TunFirewallPlan) string {
	return strings.TrimSpace(plan.Family + " " + plan.Table)
}

func dnsSearchDomains(plan planner.TunDNSPlan) []string {
	if plan.Action != planner.DNSActionConfigure || plan.TargetLink == "" {
		return nil
	}
	return []string{dnsRouteOnlyDomain}
}

func transactionNow(store txstate.TransactionStore) time.Time {
	if store.Now != nil {
		return store.Now().UTC)
	}
	return time.Now().UTC()
}
