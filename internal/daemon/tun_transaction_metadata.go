package daemon

import (
	"fmt"
	"strings"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

const xrayTunInboundOwner = "xray:tun-inbound"
const hostRoutePrerequisiteOwner = "host:route-prerequisite"

func snapshotMetadata(s netsnapshot.Snapshot, now time.Time) txstate.SnapshotMetadata {
	summary := []string{
		"default IPv4 route: " + string(s.DefaultIPv4.Status),
		"server route: " + string(s.ServerRoute.Status),
		fmt.Sprintf("tun devices inspected: %d", len(s.TunDevices)),
	}
	return txstate.SnapshotMetadata{CapturedAt: now.UTC(), Source: "daemon network snapshot", Summary: summary}
}

func desiredPlanFromTunPlan(plan planner.TunPlan) txstate.DesiredPlan {
	routes := make([]txstate.RoutePlan, 0, len(plan.Routes))
	steps := make([]txstate.PlannedStep, 0, len(plan.Steps)+len(plan.PolicyRules)+len(plan.Firewall.Rules)+2)
	for _, route := range plan.Routes {
		owner := ""
		operation := ""
		switch {
		case planner.IsTunAddAction(route.Action):
			owner = netexecutor.OwnerRoute
			operation = "add"
		case planner.IsTunVerifyAction(route.Action):
			owner = hostRoutePrerequisiteOwner
			operation = "verify"
		default:
			continue
		}
		routes = append(routes, txstate.RoutePlan{Kind: "route", Table: route.Table, CIDR: route.Destination, Via: route.Gateway, Dev: route.Interface, Owner: owner, Operation: operation})
	}
	for _, step := range plan.Steps {
		steps = append(steps, txstate.PlannedStep{Kind: "plan", Target: planner.ModeTun, Description: step, Owner: txstate.TransactionOwner})
	}
	if plan.TunAddress.CIDR != "" {
		steps = append(steps, txstate.PlannedStep{Kind: "tun-address", Target: tunAddressTarget(plan.TunAddress), Description: plan.TunAddress.Reason, Owner: netexecutor.OwnerTunAddress})
	}
	for _, rule := range plan.PolicyRules {
		if !planner.IsTunAddAction(rule.Action) {
			continue
		}
		steps = append(steps, txstate.PlannedStep{Kind: "policy-rule", Target: policyRuleTarget(rule), Description: rule.Reason, Owner: netexecutor.OwnerPolicyRule})
	}
	if plan.DNS.Action == planner.DNSActionConfigure && plan.DNS.TargetLink != "" {
		steps = append(steps, txstate.PlannedStep{Kind: "dns", Target: plan.DNS.TargetLink, Description: plan.DNS.Reason, Owner: netexecutor.OwnerDNS})
	}
	if plan.Firewall.TableAction == planner.FirewallTableAction && plan.Firewall.Table != "" {
		steps = append(steps, txstate.PlannedStep{Kind: "nftables", Target: firewallTarget(plan.Firewall), Description: plan.Firewall.Reason, Owner: netexecutor.OwnerFirewall})
	}
	return txstate.DesiredPlan{
		PlanID: plan.ProfileID + ":" + planner.ModeTun,
		TUN: txstate.TUNDesiredState{InterfaceName: plan.TunDevice.Name, MTU: plan.TunDevice.MTU, Owner: tunDesiredOwner(plan.TunDevice.Action)},
		TUNAddress: tunAddressDesiredState(plan.TunAddress),
		Routes: routes,
		DNS: txstate.DNSPlan{Backend: plan.DNS.Backend, Link: plan.DNS.TargetLink, Servers: append([]string{}, plan.DNS.Servers...), SearchDomains: dnsSearchDomains(plan.DNS), Owner: txstate.TransactionOwner},
		NFT: txstate.NFTPlan{Family: plan.Firewall.Family, Table: plan.Firewall.Table, Chains: nftChains(plan.Firewall), Owner: netexecutor.OwnerFirewall},
		Steps: steps,
	}
}

func rollbackMetadataFromTunPlan(plan planner.TunPlan) txstate.RollbackMetadata {
	routes := make([]txstate.RouteRollback, 0, len(plan.Routes))
	for _, route := range plan.Routes {
		if !planner.IsTunAddAction(route.Action) {
			continue
		}
		routes = append(routes, txstate.RouteRollback{Table: route.Table, CIDR: route.Destination, Via: route.Gateway, Dev: route.Interface, Owner: netexecutor.OwnerRoute})
	}
	rules := make([]txstate.PolicyRuleRollback, 0, len(plan.PolicyRules))
	for _, rule := range plan.PolicyRules {
		if !planner.IsTunAddAction(rule.Action) {
			continue
		}
		rules = append(rules, policyRuleRollback(rule))
	}
	metadata := txstate.RollbackMetadata{Routes: routes, PolicyRules: rules}
	if plan.TunDevice.Name != "" && tunRollbackOwnsLink(plan.TunDevice.Action) {
		metadata.TUN = []txstate.TUNRollback{{InterfaceName: plan.TunDevice.Name, Owner: netexecutor.OwnerTunDevice}}
	}
	if tunAddressRollbackIsOwned(plan.TunAddress) {
		metadata.TUNAddresses = []txstate.TUNAddressRollback{tunAddressRollbackState(plan.TunAddress)}
	}
	if plan.DNS.Action == planner.DNSActionConfigure && plan.DNS.TargetLink != "" {
		metadata.DNS = []txstate.DNSRollback{{Backend: plan.DNS.Backend, Link: plan.DNS.TargetLink, SearchDomains: dnsSearchDomains(plan.DNS), Owner: netexecutor.OwnerDNS}}
	}
	if plan.Firewall.TableAction == planner.FirewallTableAction && plan.Firewall.Table != "" {
		metadata.NFTables = []txstate.NFTablesRollback{{Family: plan.Firewall.Family, Table: plan.Firewall.Table, Owner: netexecutor.OwnerFirewall}}
	}
	return metadata
}

func tunAddressDesiredState(plan planner.TunAddressPlan) txstate.TUNAddressDesiredState {
	if strings.TrimSpace(plan.CIDR) == "" {
		return txstate.TUNAddressDesiredState{}
	}
	return txstate.TUNAddressDesiredState{Family: plan.Family, InterfaceName: plan.Interface, CIDR: plan.CIDR, Scope: plan.Scope, LinkIndex: plan.LinkIndex, LinkKind: plan.LinkKind, AppearedAfterCore: plan.AppearedAfterCore, Owner: netexecutor.OwnerTunAddress}
}

func tunAddressRollbackIsOwned(plan planner.TunAddressPlan) bool {
	return planner.IsTunAddressAssignAction(plan.Action) && plan.LinkIndex > 0 && plan.LinkKind == "tun" && plan.AppearedAfterCore && strings.TrimSpace(plan.CIDR) != ""
}

func tunAddressRollbackState(plan planner.TunAddressPlan) txstate.TUNAddressRollback {
	return txstate.TUNAddressRollback{Family: plan.Family, InterfaceName: plan.Interface, CIDR: plan.CIDR, Scope: plan.Scope, LinkIndex: plan.LinkIndex, LinkKind: plan.LinkKind, AppearedAfterCore: plan.AppearedAfterCore, Owner: netexecutor.OwnerTunAddress}
}

func tunAddressTarget(plan planner.TunAddressPlan) string {
	return fmt.Sprintf("%s@ifindex=%d:%s", plan.Interface, plan.LinkIndex, plan.CIDR)
}

func tunDesiredOwner(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "verify", "use-existing":
		return xrayTunInboundOwner
	default:
		return netexecutor.OwnerTunDevice
	}
}

func tunRollbackOwnsLink(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "create", "add":
		return true
	default:
		return false
	}
}

func policyRuleRollback(rule planner.TunPolicyRulePlan) txstate.PolicyRuleRollback {
	rollback := txstate.PolicyRuleRollback{Priority: rule.Priority, Table: rule.Table, Owner: netexecutor.OwnerPolicyRule}
	selector := strings.TrimSpace(rule.Selector)
	switch {
	case strings.HasPrefix(selector, "to "):
		rollback.To = strings.TrimSpace(strings.TrimPrefix(selector, "to "))
	case strings.HasPrefix(selector, "from "):
		rollback.From = strings.TrimSpace(strings.TrimPrefix(selector, "from "))
	default:
		rollback.From = selector
	}
	return rollback
}

func appliedStepsFromExecutor(steps []netexecutor.Step, now time.Time) []txstate.AppliedStep {
	out := make([]txstate.AppliedStep, 0, len(steps))
	for _, step := range steps {
		out = append(out, txstate.AppliedStep{Kind: step.Kind, Target: step.Target, Description: step.Description, Owner: step.Owner, AppliedAt: now.UTC()})
	}
	return out
}
