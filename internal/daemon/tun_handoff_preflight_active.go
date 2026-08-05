package daemon

import (
	"context"
	"fmt"
	"net"
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

	tunDeviceProven := activeTransactionProvesCurrentTunDevice(tx, s)
	if tunDeviceProven {
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
		if activeTransactionOwnsStaleResource(tx, resource, tunDeviceProven) {
			continue
		}
		resources = append(resources, resource)
	}
	out.StaleResources = resources
	return out
}

func activeTransactionProvesCurrentTunDevice(tx txstate.Transaction, s netsnapshot.Snapshot) bool {
	for _, address := range tx.Rollback.TUNAddresses {
		if !ownedRollbackOwner(address.Owner, netexecutor.OwnerTunAddress) {
			continue
		}
		if address.InterfaceName != netsnapshot.DefaultTunName || address.LinkIndex <= 0 || address.LinkKind != "tun" || !address.AppearedAfterCore {
			continue
		}
		if snapshotProvesTunLinkIdentity(address, s) {
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

func activeTransactionOwnsStaleResource(tx txstate.Transaction, resource netsnapshot.StaleResource, tunDeviceProven bool) bool {
	if resource.Status != netsnapshot.StatusDetected {
		return false
	}
	switch resource.Kind {
	case "transaction-file":
		return tx.State == txstate.TransactionCommitted && strings.TrimSpace(resource.Name) == tx.ID+txstate.TransactionFileSuffix
	case "tun-device":
		return resource.Name == netsnapshot.DefaultTunName && tunDeviceProven
	case "nftables-table":
		return strings.TrimSpace(resource.Name) == netsnapshot.DefaultNFTFamily+" "+netsnapshot.DefaultNFTTable && activeTransactionOwnsNFTables(tx)
	case "route", "route-table":
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
	if !cidrMatchesRouteDestination(strings.TrimSpace(route.CIDR), strings.TrimSpace(firstNonEmpty(signal.Destination, routeDestinationFromRaw(signal.Raw)))) {
		return false
	}
	if strings.TrimSpace(route.Dev) != "" && strings.TrimSpace(signal.Interface) != strings.TrimSpace(route.Dev) {
		return false
	}
	if strings.TrimSpace(route.Via) != "" && strings.TrimSpace(signal.Gateway) != strings.TrimSpace(route.Via) {
		return false
	}
	if strings.TrimSpace(route.Via) == "" && strings.TrimSpace(signal.Gateway) != "" {
		return false
	}
	return true
}

func routeResourceMatchesRollback(resource netsnapshot.StaleResource, route txstate.RouteRollback) bool {
	if strings.TrimSpace(resource.Detail) == "" {
		return false
	}
	signal, ok := parseCurrentRouteSignal(resource.Detail)
	if !ok {
		return false
	}
	return routeSignalMatchesRollback(signal, route)
}

func ruleSignalMatchesRollback(signal netsnapshot.PolicyRoutingSignal, rule txstate.PolicyRuleRollback) bool {
	if signal.Priority != "" && signal.Priority != strconv.Itoa(rule.Priority) {
		return false
	}
	if strings.TrimSpace(signal.Table) != strings.TrimSpace(rule.Table) {
		return false
	}
	selector := strings.TrimSpace(signal.Selector)
	if rule.From != "" && selector != "from "+strings.TrimSpace(rule.From) {
		return false
	}
	if rule.To != "" && selector != "to "+strings.TrimSpace(rule.To) {
		return false
	}
	if strings.TrimSpace(rule.Mark) != strings.TrimSpace(signal.Fwmark) {
		return false
	}
	return true
}

func ruleResourceMatchesRollback(resource netsnapshot.StaleResource, rule txstate.PolicyRuleRollback) bool {
	if strings.TrimSpace(resource.Detail) == "" {
		return false
	}
	signal, ok := parseCurrentPolicyRuleSignal(resource.Detail)
	if !ok {
		return false
	}
	return ruleSignalMatchesRollback(signal, rule)
}

func parseCurrentRouteSignal(line string) (netsnapshot.PolicyRoutingSignal, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return netsnapshot.PolicyRoutingSignal{}, false
	}
	destinationIndex := 0
	if isRouteTypeToken(fields[0]) {
		destinationIndex = 1
	}
	if destinationIndex >= len(fields) {
		return netsnapshot.PolicyRoutingSignal{}, false
	}
	signal := netsnapshot.PolicyRoutingSignal{Kind: "route", Raw: strings.TrimSpace(line), Destination: normalizeRouteDestination(fields[destinationIndex]), Table: "main"}
	for i := destinationIndex + 1; i+1 < len(fields); i++ {
		switch fields[i] {
		case "dev":
			signal.Interface = strings.Split(fields[i+1], "@")[0]
		case "via":
			signal.Gateway = fields[i+1]
		case "table":
			signal.Table = fields[i+1]
		}
	}
	return signal, strings.TrimSpace(signal.Table) != ""
}

func parseCurrentPolicyRuleSignal(line string) (netsnapshot.PolicyRoutingSignal, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return netsnapshot.PolicyRoutingSignal{}, false
	}
	signal := netsnapshot.PolicyRoutingSignal{Kind: "rule", Raw: strings.TrimSpace(line), Priority: strings.TrimSuffix(fields[0], ":")}
	for i := 0; i < len(fields)-1; i++ {
		switch fields[i] {
		case "lookup", "table":
			signal.Table = fields[i+1]
		case "fwmark":
			signal.Fwmark = fields[i+1]
		case "from", "to":
			if signal.Selector == "" {
				signal.Selector = fields[i] + " " + fields[i+1]
			} else {
				signal.Selector += " " + fields[i] + " " + fields[i+1]
			}
		}
	}
	return signal, signal.Priority != "" && signal.Table != ""
}

func routeDestinationFromRaw(raw string) string {
	signal, ok := parseCurrentRouteSignal(raw)
	if !ok {
		return ""
	}
	return signal.Destination
}

func normalizeRouteDestination(destination string) string {
	destination = strings.TrimSpace(destination)
	if destination == "" || destination == "default" {
		return destination
	}
	if ip := net.ParseIP(destination); ip != nil && ip.To4() != nil {
		return ip.String() + "/32"
	}
	return destination
}

func cidrMatchesRouteDestination(expected, current string) bool {
	expected = normalizeRouteDestination(expected)
	current = normalizeRouteDestination(current)
	return expected != "" && expected == current
}

func isRouteTypeToken(value string) bool {
	switch value {
	case "local", "broadcast", "unreachable", "blackhole", "prohibit", "throw", "nat", "multicast", "anycast", "unicast":
		return true
	default:
		return false
	}
}

func ownedRollbackOwner(owner, expected string) bool {
	owner = strings.TrimSpace(owner)
	expected = strings.TrimSpace(expected)
	return expected != "" && (owner == expected || owner == txstate.TransactionOwner)
}
