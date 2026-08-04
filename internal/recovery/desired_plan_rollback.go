package recovery

import (
	"fmt"
	"strconv"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// recoveryRollbackMetadata preserves durable rollback ownership and adds only
// the narrow applying-state TUN-address syscall/persistence candidate. Desired
// routes, policy rules, DNS, nftables, and link intent never grant cleanup
// authority.
func recoveryRollbackMetadata(tx txstate.Transaction) txstate.RollbackMetadata {
	rollback := tx.Rollback
	if reasons := rollbackOwnershipConsistencyReasons(tx, rollback); len(reasons) > 0 {
		return mutationFreeAmbiguousRollback(rollback, reasons)
	}
	desired := tx.DesiredPlan

	// Desired state validates the shape of a resource but never proves that the
	// daemon created it. The sole exception is the address syscall/persistence
	// crash window while applying: the exact bound link identity was persisted
	// before the mutable command and remains subject to fail-closed host checks.
	if len(rollback.TUNAddresses) == 0 && tx.State == txstate.TransactionApplying {
		address := desired.TUNAddress
		if address.InterfaceName == managedInterface &&
			address.LinkIndex > 0 &&
			address.LinkKind == "tun" &&
			address.AppearedAfterCore &&
			strings.TrimSpace(address.CIDR) == planner.DefaultTunIPv4CIDR &&
			ownedRollbackMetadata(address.Owner, netexecutor.OwnerTunAddress) {
			rollback.TUNAddresses = []txstate.TUNAddressRollback{{
				Family:            address.Family,
				InterfaceName:     address.InterfaceName,
				CIDR:              address.CIDR,
				Scope:             address.Scope,
				LinkIndex:         address.LinkIndex,
				LinkKind:          address.LinkKind,
				AppearedAfterCore: address.AppearedAfterCore,
				Owner:             address.Owner,
			}}
		}
	}
	return rollback
}

func rollbackOwnershipConsistencyReasons(tx txstate.Transaction, rollback txstate.RollbackMetadata) []string {
	if !rollbackHasNetworkOwnership(rollback) {
		return nil
	}
	if tx.State == txstate.TransactionPlanned {
		return []string{"planned transaction cannot authorize network cleanup"}
	}
	var reasons []string
	if tx.State == txstate.TransactionApplying {
		reasons = append(reasons, applyingRollbackDesiredMismatches(tx, rollback)...)
	}
	reasons = append(reasons, rollbackAppliedProofMismatches(tx.AppliedSteps, rollback)...)
	return compactReasonStrings(reasons)
}

func mutationFreeAmbiguousRollback(rollback txstate.RollbackMetadata, reasons []string) txstate.RollbackMetadata {
	out := txstate.RollbackMetadata{
		GeneratedConfigs: append([]txstate.GeneratedConfigRollback(nil), rollback.GeneratedConfigs...),
		ChildProcesses:   append([]txstate.ChildProcessRollback(nil), rollback.ChildProcesses...),
	}
	out.ChildProcesses = append(out.ChildProcesses, txstate.ChildProcessRollback{
		Label: "ownership-consistency: " + strings.Join(compactReasonStrings(reasons), "; "),
		Owner: txstate.TransactionOwner,
	})
	return out
}

func rollbackHasNetworkOwnership(rollback txstate.RollbackMetadata) bool {
	return len(rollback.TUN) > 0 || len(rollback.TUNAddresses) > 0 || len(rollback.Routes) > 0 || len(rollback.PolicyRules) > 0 || len(rollback.DNS) > 0 || len(rollback.NFTables) > 0
}

func applyingRollbackDesiredMismatches(tx txstate.Transaction, rollback txstate.RollbackMetadata) []string {
	var reasons []string
	if len(rollback.Routes) > 0 && !stringCounterEqual(desiredRouteCounter(tx.DesiredPlan.Routes), rollbackRouteCounter(rollback.Routes)) {
		reasons = append(reasons, "route rollback multiset does not match desired route multiset")
	}
	if len(rollback.PolicyRules) > 0 && !stringCounterEqual(desiredPolicyRuleCounter(tx.DesiredPlan.Steps), rollbackPolicyRuleCounter(rollback.PolicyRules)) {
		reasons = append(reasons, "policy-rule rollback multiset does not match desired policy-rule multiset")
	}
	if len(rollback.DNS) > 0 && !stringCounterEqual(desiredDNSCounter(tx.DesiredPlan.DNS), rollbackDNSCounter(rollback.DNS)) {
		reasons = append(reasons, "DNS rollback multiset does not match desired DNS multiset")
	}
	if len(rollback.NFTables) > 0 && !stringCounterEqual(desiredNFTCounter(tx.DesiredPlan.NFT), rollbackNFTCounter(rollback.NFTables)) {
		reasons = append(reasons, "nftables rollback multiset does not match desired nftables multiset")
	}
	return reasons
}

func rollbackAppliedProofMismatches(applied []txstate.AppliedStep, rollback txstate.RollbackMetadata) []string {
	proofs := appliedStepCounter(applied)
	var reasons []string
	consume := func(kind, target, owner string) {
		if appliedStepCounterConsume(proofs, kind, target, owner) {
			return
		}
		reasons = append(reasons, fmt.Sprintf("rollback tuple lacks exact applied proof kind=%s target=%s", kind, target))
	}
	for _, item := range rollback.TUN {
		consume("tun-device", strings.TrimSpace(item.InterfaceName), netexecutor.OwnerTunDevice)
	}
	for _, item := range rollback.TUNAddresses {
		consume("tun-address", tunAddressRollbackTarget(item), netexecutor.OwnerTunAddress)
	}
	for _, item := range rollback.Routes {
		consume("route", routeRollbackTarget(item), netexecutor.OwnerRoute)
	}
	for _, item := range rollback.PolicyRules {
		consume("policy-rule", policyRuleRollbackTarget(item), netexecutor.OwnerPolicyRule)
	}
	for _, item := range rollback.DNS {
		consume("dns", strings.TrimSpace(item.Link), netexecutor.OwnerDNS)
	}
	for _, item := range rollback.NFTables {
		consume("nftables", nftRollbackTarget(item), netexecutor.OwnerFirewall)
	}
	return reasons
}

func appliedStepCounter(applied []txstate.AppliedStep) map[string]int {
	out := make(map[string]int, len(applied))
	for _, step := range applied {
		out[appliedStepKey(step.Kind, step.Target, step.Owner)]++
	}
	return out
}

func appliedStepCounterConsume(counter map[string]int, kind, target, owner string) bool {
	for _, candidateOwner := range []string{owner, txstate.TransactionOwner} {
		key := appliedStepKey(kind, target, candidateOwner)
		if counter[key] <= 0 {
			continue
		}
		counter[key]--
		return true
	}
	return false
}

func appliedStepKey(kind, target, owner string) string {
	return strings.TrimSpace(kind) + "\x00" + strings.TrimSpace(target) + "\x00" + strings.TrimSpace(owner)
}

func tunAddressRollbackTarget(item txstate.TUNAddressRollback) string {
	return fmt.Sprintf("%s@ifindex=%d:%s", strings.TrimSpace(item.InterfaceName), item.LinkIndex, strings.TrimSpace(item.CIDR))
}

func routeRollbackTarget(item txstate.RouteRollback) string {
	return strings.TrimSpace(item.Table) + " " + strings.TrimSpace(item.CIDR)
}

func policyRuleRollbackTarget(item txstate.PolicyRuleRollback) string {
	selector := strings.TrimSpace(item.From)
	if selector != "" {
		selector = "from " + selector
	} else if to := strings.TrimSpace(item.To); to != "" {
		selector = "to " + to
	} else if mark := strings.TrimSpace(item.Mark); mark != "" {
		selector = "fwmark " + mark
	}
	return fmt.Sprintf("priority %d %s lookup %s", item.Priority, selector, strings.TrimSpace(item.Table))
}

func nftRollbackTarget(item txstate.NFTablesRollback) string {
	return strings.TrimSpace(item.Family) + " " + strings.TrimSpace(item.Table)
}

func desiredRouteCounter(routes []txstate.RoutePlan) map[string]int {
	out := map[string]int{}
	for _, item := range routes {
		if item.Operation != "add" || !ownedRollbackMetadata(item.Owner, netexecutor.OwnerRoute) {
			continue
		}
		out[routeRollbackKey(txstate.RouteRollback{Table: item.Table, CIDR: item.CIDR, Via: item.Via, Dev: item.Dev, Owner: item.Owner})]++
	}
	return out
}

func rollbackRouteCounter(routes []txstate.RouteRollback) map[string]int {
	out := map[string]int{}
	for _, item := range routes {
		out[routeRollbackKey(item)]++
	}
	return out
}

func routeRollbackKey(item txstate.RouteRollback) string {
	return strings.Join([]string{strings.TrimSpace(item.Table), strings.TrimSpace(item.CIDR), strings.TrimSpace(item.Via), strings.TrimSpace(item.Dev), normalizedRollbackOwner(item.Owner, netexecutor.OwnerRoute)}, "\x00")
}

func desiredPolicyRuleCounter(steps []txstate.PlannedStep) map[string]int {
	out := map[string]int{}
	for _, step := range steps {
		item, ok := desiredPolicyRuleRollback(step)
		if !ok {
			continue
		}
		out[policyRuleRollbackKey(item)]++
	}
	return out
}

func rollbackPolicyRuleCounter(rules []txstate.PolicyRuleRollback) map[string]int {
	out := map[string]int{}
	for _, item := range rules {
		out[policyRuleRollbackKey(item)]++
	}
	return out
}

func policyRuleRollbackKey(item txstate.PolicyRuleRollback) string {
	return strings.Join([]string{strconv.Itoa(item.Priority), strings.TrimSpace(item.From), strings.TrimSpace(item.To), strings.TrimSpace(item.Table), strings.TrimSpace(item.Mark), normalizedRollbackOwner(item.Owner, netexecutor.OwnerPolicyRule)}, "\x00")
}

func desiredDNSCounter(dns txstate.DNSPlan) map[string]int {
	out := map[string]int{}
	if ownedRollbackMetadata(dns.Owner, netexecutor.OwnerDNS) && strings.TrimSpace(dns.Link) != "" {
		out[dnsRollbackKey(txstate.DNSRollback{Backend: dns.Backend, Link: dns.Link, SearchDomains: dns.SearchDomains, Owner: dns.Owner})]++
	}
	return out
}

func rollbackDNSCounter(items []txstate.DNSRollback) map[string]int {
	out := map[string]int{}
	for _, item := range items {
		out[dnsRollbackKey(item)]++
	}
	return out
}

func dnsRollbackKey(item txstate.DNSRollback) string {
	return strings.Join([]string{strings.TrimSpace(item.Backend), strings.TrimSpace(item.Link), strings.Join(item.SearchDomains, ","), normalizedRollbackOwner(item.Owner, netexecutor.OwnerDNS)}, "\x00")
}

func desiredNFTCounter(nft txstate.NFTPlan) map[string]int {
	out := map[string]int{}
	if ownedRollbackMetadata(nft.Owner, netexecutor.OwnerFirewall) && strings.TrimSpace(nft.Family) != "" && strings.TrimSpace(nft.Table) != "" {
		out[nftRollbackKey(txstate.NFTablesRollback{Family: nft.Family, Table: nft.Table, Owner: nft.Owner})]++
	}
	return out
}

func rollbackNFTCounter(items []txstate.NFTablesRollback) map[string]int {
	out := map[string]int{}
	for _, item := range items {
		out[nftRollbackKey(item)]++
	}
	return out
}

func nftRollbackKey(item txstate.NFTablesRollback) string {
	return strings.Join([]string{strings.TrimSpace(item.Family), strings.TrimSpace(item.Table), normalizedRollbackOwner(item.Owner, netexecutor.OwnerFirewall)}, "\x00")
}

func normalizedRollbackOwner(owner, expected string) string {
	if strings.TrimSpace(owner) == txstate.TransactionOwner {
		return expected
	}
	return strings.TrimSpace(owner)
}

func stringCounterEqual(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for key, count := range left {
		if right[key] != count {
			return false
		}
	}
	return true
}

func compactReasonStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func desiredPolicyRuleRollback(step txstate.PlannedStep) (txstate.PolicyRuleRollback, bool) {
	if step.Kind != "policy-rule" || !ownedRollbackMetadata(step.Owner, netexecutor.OwnerPolicyRule) {
		return txstate.PolicyRuleRollback{}, false
	}
	fields := strings.Fields(step.Target)
	if len(fields) != 6 || fields[0] != "priority" || fields[4] != "lookup" {
		return txstate.PolicyRuleRollback{}, false
	}
	priority, err := strconv.Atoi(fields[1])
	if err != nil {
		return txstate.PolicyRuleRollback{}, false
	}
	rule := txstate.PolicyRuleRollback{Priority: priority, Table: fields[5], Owner: step.Owner}
	switch fields[2] {
	case "from":
		rule.From = fields[3]
	case "to":
		rule.To = fields[3]
	default:
		return txstate.PolicyRuleRollback{}, false
	}
	return rule, true
}

func reservedTunPolicyRule(rule txstate.PolicyRuleRollback) bool {
	defaultFrom := strings.TrimSpace(strings.TrimPrefix(planner.IPv4DefaultSelector, "from "))
	return rule.Priority == planner.TunRulePriority &&
		rule.Table == planner.TunRoutingTable &&
		strings.TrimSpace(rule.From) == defaultFrom &&
		strings.TrimSpace(rule.To) == "" &&
		strings.TrimSpace(rule.Mark) == ""
}

const normalizedSystemdResolvedBackend = "systemd-resolved"

func systemdResolvedBackend(backend string) bool {
	backend = strings.TrimSpace(backend)
	return backend == "" || backend == normalizedSystemdResolvedBackend || backend == planner.DNSBackendSystemdResolved
}
