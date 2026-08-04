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
	rollback := tx.Rollback
	plan := planner.TunPlan{Mode: tx.Mode, ProfileID: tx.ProfileID}
	if len(rollback.TUN) > 0 {
		plan.TunDevice = planner.TunDevicePlan{Name: rollback.TUN[0].InterfaceName, MTU: tx.DesiredPlan.TUN.MTU, Action: "add"}
	}
	if len(rollback.TUNAddresses) > 0 {
		address := rollback.TUNAddresses[0]
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
	}
	for _, route := range rollback.Routes {
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
		dns := rollback.DNS[0]
		plan.DNS = planner.TunDNSPlan{
			Backend:    dns.Backend,
			TargetLink: dns.Link,
			Servers:    append([]string{}, tx.DesiredPlan.DNS.Servers...),
			Action:     planner.DNSActionConfigure,
		}
	}
	if len(rollback.NFTables) > 0 {
		nft := rollback.NFTables[0]
		plan.Firewall = planner.TunFirewallPlan{
			Backend:     planner.FirewallBackendNftables,
			Family:      nft.Family,
			Table:       nft.Table,
			TableAction: planner.FirewallTableAction,
		}
	}
	return plan
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
		return store.Now().UTC()
	}
	return time.Now().UTC()
}
