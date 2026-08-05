package daemon

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func (m *XrayManager) preflightActiveReplacementOwnership(ctx context.Context, s netsnapshot.Snapshot, handoff string) error {
	policy := api.NormalizeHandoffPolicy(handoff)
	if policy != api.HandoffReplacePodlaz {
		return nil
	}
	status := m.statusForPublication(ctx)
	tx, ok, err := activeCommittedTransaction(status, m.runtimeDir())
	if err != nil || !ok {
		detail := "active podlaz TUN transaction identity could not be proven"
		if err != nil {
			detail = err.Error()
		}
		return &tunHandoffBlocker{
			Policy:    policy,
			Conflicts: []string{detail},
			NextStep:  "Resolve the active transaction identity before replacing the active podlaz TUN session.",
		}
	}

	inspected := m.withPodlazRuntimeStaleState(ctx, s)
	inspected = snapshotWithoutActiveTransactionOwnership(inspected, tx)
	if blocker := stalePodlazStateBlocker(inspected); blocker != nil {
		return blocker
	}
	conflicts := foreignOwnershipConflicts(inspected)
	if len(conflicts) == 0 {
		return nil
	}
	return &tunHandoffBlocker{
		Policy:    policy,
		Conflicts: conflicts,
		NextStep:  "Resolve unrelated foreign VPN/DNS/routing ownership before replacing the active podlaz TUN session.",
	}
}

func snapshotWithoutActiveTransactionOwnership(s netsnapshot.Snapshot, tx txstate.Transaction) netsnapshot.Snapshot {
	out := s
	out.TunDevices = append([]netsnapshot.TunDevice(nil), s.TunDevices...)
	out.PolicyRouting = append([]netsnapshot.PolicyRoutingSignal(nil), s.PolicyRouting...)
	out.StaleResources = append([]netsnapshot.StaleResource(nil), s.StaleResources...)

	if activeTransactionOwnsTunDevice(tx) {
		devices := out.TunDevices[:0]
		for _, device := range out.TunDevices {
			if device.Name == netsnapshot.DefaultTunName && device.Status == netsnapshot.StatusDetected {
				continue
			}
			devices = append(devices, device)
		}
		out.TunDevices = devices
	}
	if activeTransactionOwnsNFTables(tx) {
		out.Nftables.PodlazTable = netsnapshot.Finding{Status: netsnapshot.StatusMissing, Summary: "active transaction owns podlaz nftables table"}
	}
	if activeTransactionOwnsPolicyRouting(tx) {
		signals := out.PolicyRouting[:0]
		for _, signal := range out.PolicyRouting {
			if activeTransactionOwnsPolicyRoutingSignal(tx, signal) {
				continue
			}
			signals = append(signals, signal)
		}
		out.PolicyRouting = signals
	}

	resources := out.StaleResources[:0]
	for _, resource := range out.StaleResources {
		if activeTransactionOwnsStaleResource(tx, resource) {
			continue
		}
		resources = append(resources, resource)
	}
	out.StaleResources = resources
	return out
}

func activeTransactionOwnsTunDevice(tx txstate.Transaction) bool {
	if tx.DesiredPlan.TUN.InterfaceName == netsnapshot.DefaultTunName && strings.TrimSpace(tx.DesiredPlan.TUN.Owner) != "" {
		return true
	}
	for _, entry := range tx.Rollback.TUN {
		if entry.InterfaceName == netsnapshot.DefaultTunName && ownedRollbackOwner(entry.Owner, netexecutor.OwnerTunDevice) {
			return true
		}
	}
	return false
}

func activeTransactionOwnsNFTables(tx txstate.Transaction) bool {
	for _, entry := range tx.Rollback.NFTables {
		if ownedRollbackOwner(entry.Owner, netexecutor.OwnerFirewall) && strings.TrimSpace(entry.Family) == netsnapshot.DefaultNFTFamily && strings.TrimSpace(entry.Table) == netsnapshot.DefaultNFTTable {
			return true
		}
	}
	return false
}

func activeTransactionOwnsPolicyRouting(tx txstate.Transaction) bool {
	return len(tx.Rollback.Routes) > 0 || len(tx.Rollback.PolicyRules) > 0
}

func activeTransactionOwnsPolicyRoutingSignal(tx txstate.Transaction, signal netsnapshot.PolicyRoutingSignal) bool {
	switch signal.Kind {
	case "route":
		for _, route := range tx.Rollback.Routes {
			if !ownedRollbackOwner(route.Owner, netexecutor.OwnerRoute) {
				continue
			}
			if routeSignalMatchesRollback(signal, route) {
				return true
			}
		}
	case "rule":
		for _, rule := range tx.Rollback.PolicyRules {
			if !ownedRollbackOwner(rule.Owner, netexecutor.OwnerPolicyRule) {
				continue
			}
			if ruleSignalMatchesRollback(signal, rule) {
				return true
			}
		}
	}
	return false
}

func activeTransactionOwnsStaleResource(tx txstate.Transaction, resource netsnapshot.StaleResource) bool {
	if resource.Status != netsnapshot.StatusDetected {
		return false
	}
	switch resource.Kind {
	case "tun-device":
		return resource.Name == netsnapshot.DefaultTunName && activeTransactionOwnsTunDevice(tx)
	case "nftables-table":
		return strings.TrimSpace(resource.Name) == netsnapshot.DefaultNFTFamily+" "+netsnapshot.DefaultNFTTable && activeTransactionOwnsNFTables(tx)
	case "route-table":
		for _, route := range tx.Rollback.Routes {
			if !ownedRollbackOwner(route.Owner, netexecutor.OwnerRoute) {
				continue
			}
			if routeResourceMatchesRollback(resource, route) {
				return true
			}
		}
	case "policy-rule":
		for _, rule := range tx.Rollback.PolicyRules {
			if !ownedRollbackOwner(rule.Owner, netexecutor.OwnerPolicyRule) {
				continue
			}
			if ruleResourceMatchesRollback(resource, rule) {
				return true
			}
		}
	}
	return false
}

func routeSignalMatchesRollback(signal netsnapshot.PolicyRoutingSignal, route txstate.RouteRollback) bool {
	if strings.TrimSpace(signal.Table) != strings.TrimSpace(route.Table) {
		return false
	}
	raw := " " + strings.TrimSpace(signal.Raw) + " "
	if strings.TrimSpace(route.CIDR) != "" && !strings.Contains(raw, " "+strings.TrimSpace(route.CIDR)+" ") {
		return false
	}
	if strings.TrimSpace(route.Dev) != "" && strings.TrimSpace(signal.Interface) != strings.TrimSpace(route.Dev) {
		return false
	}
	if strings.TrimSpace(route.Via) != "" && !strings.Contains(raw, " via "+strings.TrimSpace(route.Via)+" ") {
		return false
	}
	return true
}

func routeResourceMatchesRollback(resource netsnapshot.StaleResource, route txstate.RouteRollback) bool {
	if strings.TrimSpace(resource.Name) != strings.TrimSpace(route.Table) {
		return false
	}
	if strings.TrimSpace(resource.Detail) == "" {
		return false
	}
	return routeSignalMatchesRollback(netsnapshot.PolicyRoutingSignal{Kind: "route", Table: resource.Name, Interface: route.Dev, Raw: resource.Detail}, route)
}

func ruleSignalMatchesRollback(signal netsnapshot.PolicyRoutingSignal, rule txstate.PolicyRuleRollback) bool {
	if signal.Priority != "" && signal.Priority != strconv.Itoa(rule.Priority) {
		return false
	}
	if strings.TrimSpace(signal.Table) != "" && strings.TrimSpace(signal.Table) != strings.TrimSpace(rule.Table) {
		return false
	}
	selector := strings.TrimSpace(signal.Selector)
	if rule.From != "" && selector != "from "+strings.TrimSpace(rule.From) {
		return false
	}
	if rule.To != "" && selector != "to "+strings.TrimSpace(rule.To) {
		return false
	}
	if rule.Mark != "" && strings.TrimSpace(signal.Fwmark) != strings.TrimSpace(rule.Mark) {
		return false
	}
	return true
}

func ruleResourceMatchesRollback(resource netsnapshot.StaleResource, rule txstate.PolicyRuleRollback) bool {
	if resource.Name != "" && resource.Name != strconv.Itoa(rule.Priority) {
		return false
	}
	if strings.TrimSpace(resource.Detail) == "" {
		return false
	}
	return ruleSignalMatchesRollback(netsnapshot.PolicyRoutingSignal{Kind: "rule", Priority: resource.Name, Raw: resource.Detail}, rule)
}

func ownedRollbackOwner(owner, expected string) bool {
	owner = strings.TrimSpace(owner)
	expected = strings.TrimSpace(expected)
	return expected != "" && (owner == expected || owner == txstate.TransactionOwner)
}
