package daemon

import (
	"context"
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

	// Replacement authority is limited to the exact active Podlaz transaction.
	// Subtract that transaction's proved projection, then fail only on remaining
	// Podlaz-owned/recovery state. Unrelated TUN/DNS/routing/firewall state is the
	// host baseline for the next collision-free allocation and is not a blocker.
	inspected := m.withPodlazRuntimeStaleState(ctx, s)
	inspected = snapshotWithoutActiveTransactionOwnership(inspected, tx)
	if blocker := stalePodlazStateBlocker(inspected); blocker != nil {
		return blocker
	}
	return nil
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

	routeSignals := activeRouteRollbackCounter(tx)
	ruleSignals := activePolicyRuleRollbackCounter(tx)
	signals := out.PolicyRouting[:0]
	for _, signal := range out.PolicyRouting {
		if consumeActivePolicyRoutingSignal(signal, routeSignals, ruleSignals) {
			continue
		}
		signals = append(signals, signal)
	}
	out.PolicyRouting = signals

	routeResources := activeRouteRollbackCounter(tx)
	ruleResources := activePolicyRuleRollbackCounter(tx)
	resources := out.StaleResources[:0]
	for _, resource := range out.StaleResources {
		if activeTransactionOwnsStaleResource(tx, resource, tunDeviceProven, routeResources, ruleResources) {
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

func activeTransactionOwnsStaleResource(tx txstate.Transaction, resource netsnapshot.StaleResource, tunDeviceProven bool, routeCounter, ruleCounter map[string]int) bool {
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
		current, ok := parseCurrentRouteEvidence(resource.Detail, resource.Name)
		return ok && consumeCounter(routeCounter, currentRouteKey(current))
	case "policy-rule":
		current, ok := parseCurrentPolicyRuleEvidence(resource.Detail)
		return ok && consumeCounter(ruleCounter, currentRuleKey(current))
	}
	return false
}

func consumeActivePolicyRoutingSignal(signal netsnapshot.PolicyRoutingSignal, routeCounter, ruleCounter map[string]int) bool {
	switch signal.Kind {
	case "route":
		current, ok := parseCurrentRouteEvidence(signal.Raw, signal.Table)
		return ok && consumeCounter(routeCounter, currentRouteKey(current))
	case "rule":
		current, ok := parseCurrentPolicyRuleEvidence(signal.Raw)
		return ok && consumeCounter(ruleCounter, currentRuleKey(current))
	default:
		return false
	}
}

func consumeCounter(counter map[string]int, key string) bool {
	if key == "" || counter[key] <= 0 {
		return false
	}
	counter[key]--
	if counter[key] == 0 {
		delete(counter, key)
	}
	return true
}

func activeRouteRollbackCounter(tx txstate.Transaction) map[string]int {
	out := map[string]int{}
	for _, route := range tx.Rollback.Routes {
		if !ownedRollbackOwner(route.Owner, netexecutor.OwnerRoute) {
			continue
		}
		out[rollbackRouteKey(route)]++
	}
	return out
}

func activePolicyRuleRollbackCounter(tx txstate.Transaction) map[string]int {
	out := map[string]int{}
	for _, rule := range tx.Rollback.PolicyRules {
		if !ownedRollbackOwner(rule.Owner, netexecutor.OwnerPolicyRule) {
			continue
		}
		out[rollbackRuleKey(rule)]++
	}
	return out
}

type currentRouteEvidence struct {
	Destination string
	Table       string
	Dev         string
	Via         string
	Raw         string
}

func parseCurrentRouteEvidence(line, tableHint string) (currentRouteEvidence, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return currentRouteEvidence{}, false
	}
	destinationIndex := 0
	if isRouteTypeToken(fields[0]) {
		destinationIndex = 1
	}
	if destinationIndex >= len(fields) {
		return currentRouteEvidence{}, false
	}
	evidence := currentRouteEvidence{
		Destination: normalizeRouteDestination(fields[destinationIndex]),
		Table:       canonicalRoutingTable(tableHint),
		Raw:         strings.TrimSpace(line),
	}
	if evidence.Table == "" {
		evidence.Table = "main"
	}
	for i := destinationIndex + 1; i+1 < len(fields); i++ {
		switch fields[i] {
		case "dev":
			evidence.Dev = strings.Split(fields[i+1], "@")[0]
		case "via":
			evidence.Via = fields[i+1]
		case "table":
			evidence.Table = canonicalRoutingTable(fields[i+1])
		}
	}
	return evidence, strings.TrimSpace(evidence.Table) != "" && strings.TrimSpace(evidence.Destination) != ""
}

type currentRuleEvidence struct {
	Priority string
	From     string
	To       string
	Table    string
	Fwmark   string
	Raw      string
}

func parseCurrentPolicyRuleEvidence(line string) (currentRuleEvidence, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return currentRuleEvidence{}, false
	}
	evidence := currentRuleEvidence{Raw: strings.TrimSpace(line), Priority: strings.TrimSuffix(fields[0], ":")}
	for i := 0; i < len(fields)-1; i++ {
		switch fields[i] {
		case "lookup", "table":
			evidence.Table = canonicalRoutingTable(fields[i+1])
		case "fwmark":
			evidence.Fwmark = fields[i+1]
		case "from":
			evidence.From = fields[i+1]
		case "to":
			evidence.To = fields[i+1]
		}
	}
	return evidence, evidence.Priority != "" && evidence.Table != ""
}

func rollbackRouteKey(route txstate.RouteRollback) string {
	return strings.Join([]string{
		canonicalRoutingTable(route.Table),
		normalizeRouteDestination(route.CIDR),
		strings.TrimSpace(route.Via),
		strings.TrimSpace(route.Dev),
	}, "\x00")
}

func currentRouteKey(route currentRouteEvidence) string {
	return strings.Join([]string{
		canonicalRoutingTable(route.Table),
		normalizeRouteDestination(route.Destination),
		strings.TrimSpace(route.Via),
		strings.TrimSpace(route.Dev),
	}, "\x00")
}

func rollbackRuleKey(rule txstate.PolicyRuleRollback) string {
	from := normalizeRuleSelector(rule.From)
	to := normalizeRuleSelector(rule.To)
	if from == "all" && to != "" {
		from = ""
	}
	return strings.Join([]string{
		strconv.Itoa(rule.Priority),
		from,
		to,
		canonicalRoutingTable(rule.Table),
		strings.TrimSpace(rule.Mark),
	}, "\x00")
}

func currentRuleKey(rule currentRuleEvidence) string {
	from := normalizeRuleSelector(rule.From)
	to := normalizeRuleSelector(rule.To)
	if from == "all" && to != "" {
		from = ""
	}
	return strings.Join([]string{
		strings.TrimSpace(rule.Priority),
		from,
		to,
		canonicalRoutingTable(rule.Table),
		strings.TrimSpace(rule.Fwmark),
	}, "\x00")
}

func canonicalRoutingTable(table string) string {
	switch strings.TrimSpace(table) {
	case "podlaz", netsnapshot.DefaultRouteTableID:
		return "podlaz"
	default:
		return strings.TrimSpace(table)
	}
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

func normalizeRuleSelector(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "all" {
		return value
	}
	if ip := net.ParseIP(value); ip != nil && ip.To4() != nil {
		return ip.String() + "/32"
	}
	if ip, network, err := net.ParseCIDR(value); err == nil && ip.To4() != nil {
		ones, bits := network.Mask.Size()
		if bits == 32 && ones == 32 {
			return ip.String() + "/32"
		}
	}
	return value
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
