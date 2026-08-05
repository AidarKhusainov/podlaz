package recovery

import (
	"fmt"
	"strconv"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type RollbackProjection struct {
	Rollback   txstate.RollbackMetadata
	Incomplete bool
	Reasons    []string
}

func ProjectRollbackMetadata(tx txstate.Transaction) RollbackProjection {
	projection := projectRollbackMetadataWithoutApplyingGaps(tx)
	if projection.Incomplete {
		return projection
	}
	gaps := applyingDesiredOwnershipGaps(tx)
	if len(gaps) > 0 {
		projection.Incomplete = true
		projection.Reasons = append(projection.Reasons, gaps...)
	}
	return projection
}

// recoveryRollbackMetadata preserves durable rollback ownership and adds only
// the narrow applying-state TUN-address syscall/persistence candidate. Desired
// routes, policy rules, DNS, nftables, and link intent never grant cleanup
// authority.
func recoveryRollbackMetadata(tx txstate.Transaction) txstate.RollbackMetadata {
	return projectRollbackMetadataWithoutApplyingGaps(tx).Rollback
}

func projectRollbackMetadataWithoutApplyingGaps(tx txstate.Transaction) RollbackProjection {
	rollback := tx.Rollback
	if reasons := rollbackOwnershipConsistencyReasons(tx, rollback); len(reasons) > 0 {
		return RollbackProjection{Rollback: mutationFreeAmbiguousRollback(rollback, reasons), Incomplete: true, Reasons: reasons}
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
	return RollbackProjection{Rollback: rollback}
}

func rollbackOwnershipConsistencyReasons(tx txstate.Transaction, rollback txstate.RollbackMetadata) []string {
	rollbackCounter := rollbackNetworkStepCounter(rollback)
	appliedCounter := appliedNetworkStepCounter(tx.AppliedSteps)
	if len(rollbackCounter) == 0 && len(appliedCounter) == 0 {
		return nil
	}
	if tx.State == txstate.TransactionPlanned {
		return []string{"planned transaction cannot authorize network cleanup"}
	}
	var reasons []string
	reasons = append(reasons, rollbackDesiredSubsetMismatches(tx.DesiredPlan, rollback)...)
	if tx.State == txstate.TransactionApplying {
		reasons = append(reasons, applyingAppliedRollbackMismatches(appliedCounter, rollbackCounter)...)
	} else if !stringCounterEqual(appliedCounter, rollbackCounter) {
		reasons = append(reasons, "applied network ownership multiset does not match rollback network multiset")
	}
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

func rollbackDesiredSubsetMismatches(desired txstate.DesiredPlan, rollback txstate.RollbackMetadata) []string {
	var reasons []string
	if len(rollback.Routes) > 0 {
		desiredCounter := desiredRouteCounter(desired.Routes)
		if len(desiredCounter) == 0 {
			reasons = append(reasons, "route rollback requires exact desired route tuple metadata")
		} else {
			if !stringCounterSubset(rollbackRouteCounter(rollback.Routes), desiredCounter) {
				reasons = append(reasons, "route rollback multiset is not an exact subset of desired route multiset")
			}
			reasons = append(reasons, ambiguousDesiredRouteShortTargetMismatches(desired.Routes, rollback.Routes)...)
		}
	}
	if len(rollback.PolicyRules) > 0 {
		desiredCounter := desiredPolicyRuleCounter(desired.Steps)
		if len(desiredCounter) == 0 {
			reasons = append(reasons, "policy-rule rollback requires exact desired policy-rule tuple metadata")
		} else if !stringCounterSubset(rollbackPolicyRuleCounter(rollback.PolicyRules), desiredCounter) {
			reasons = append(reasons, "policy-rule rollback multiset is not an exact subset of desired policy-rule multiset")
		}
	}
	if len(rollback.DNS) > 0 {
		desiredCounter := desiredDNSCounter(desired.DNS)
		if len(desiredCounter) == 0 {
			reasons = append(reasons, "DNS rollback requires exact desired DNS tuple metadata")
		} else if !stringCounterSubset(rollbackDNSCounter(rollback.DNS), desiredCounter) {
			reasons = append(reasons, "DNS rollback multiset is not an exact subset of desired DNS multiset")
		}
	}
	if len(rollback.NFTables) > 0 {
		desiredCounter := desiredNFTCounter(desired.NFT)
		if len(desiredCounter) == 0 {
			reasons = append(reasons, "nftables rollback requires exact desired nftables tuple metadata")
		} else if !stringCounterSubset(rollbackNFTCounter(rollback.NFTables), desiredCounter) {
			reasons = append(reasons, "nftables rollback multiset is not an exact subset of desired nftables multiset")
		}
	}
	return reasons
}

func applyingAppliedRollbackMismatches(applied, rollback map[string]int) []string {
	if stringCounterEqual(applied, rollback) {
		return nil
	}
	return []string{"applied network ownership multiset does not match rollback network multiset"}
}

func appliedNetworkStepCounter(applied []txstate.AppliedStep) map[string]int {
	out := make(map[string]int, len(applied))
	for _, step := range applied {
		if !networkAppliedStep(step) {
			continue
		}
		out[appliedStepKey(step.Kind, step.Target, normalizeAppliedStepOwner(step.Owner, step.Kind))]++
	}
	return out
}

func networkAppliedStep(step txstate.AppliedStep) bool {
	switch step.Kind {
	case "tun-device", "tun-address", "route", "policy-rule", "dns", "nftables":
		return ownedNetworkStepOwner(step.Kind, step.Owner)
	default:
		return false
	}
}

func ownedNetworkStepOwner(kind, owner string) bool {
	switch kind {
	case "tun-device":
		return ownedRollbackMetadata(owner, netexecutor.OwnerTunDevice)
	case "tun-address":
		return ownedRollbackMetadata(owner, netexecutor.OwnerTunAddress)
	case "route":
		return ownedRollbackMetadata(owner, netexecutor.OwnerRoute)
	case "policy-rule":
		return ownedRollbackMetadata(owner, netexecutor.OwnerPolicyRule)
	case "dns":
		return ownedRollbackMetadata(owner, netexecutor.OwnerDNS)
	case "nftables":
		return ownedRollbackMetadata(owner, netexecutor.OwnerFirewall)
	default:
		return false
	}
}

func normalizeAppliedStepOwner(owner, kind string) string {
	switch kind {
	case "tun-device":
		return normalizedRollbackOwner(owner, netexecutor.OwnerTunDevice)
	case "tun-address":
		return normalizedRollbackOwner(owner, netexecutor.OwnerTunAddress)
	case "route":
		return normalizedRollbackOwner(owner, netexecutor.OwnerRoute)
	case "policy-rule":
		return normalizedRollbackOwner(owner, netexecutor.OwnerPolicyRule)
	case "dns":
		return normalizedRollbackOwner(owner, netexecutor.OwnerDNS)
	case "nftables":
		return normalizedRollbackOwner(owner, netexecutor.OwnerFirewall)
	default:
		return strings.TrimSpace(owner)
	}
}

func rollbackNetworkStepCounter(rollback txstate.RollbackMetadata) map[string]int {
	out := map[string]int{}
	add := func(kind, target, owner string) { out[appliedStepKey(kind, target, owner)]++ }
	for _, item := range rollback.TUN {
		if ownedRollbackMetadata(item.Owner, netexecutor.OwnerTunDevice) {
			add("tun-device", strings.TrimSpace(item.InterfaceName), netexecutor.OwnerTunDevice)
		}
	}
	for _, item := range rollback.TUNAddresses {
		if ownedRollbackMetadata(item.Owner, netexecutor.OwnerTunAddress) {
			add("tun-address", tunAddressRollbackTarget(item), netexecutor.OwnerTunAddress)
		}
	}
	for _, item := range rollback.Routes {
		if ownedRollbackMetadata(item.Owner, netexecutor.OwnerRoute) {
			add("route", routeRollbackTarget(item), netexecutor.OwnerRoute)
		}
	}
	for _, item := range rollback.PolicyRules {
		if ownedRollbackMetadata(item.Owner, netexecutor.OwnerPolicyRule) {
			add("policy-rule", policyRuleRollbackTarget(item), netexecutor.OwnerPolicyRule)
		}
	}
	for _, item := range rollback.DNS {
		if ownedRollbackMetadata(item.Owner, netexecutor.OwnerDNS) {
			add("dns", strings.TrimSpace(item.Link), netexecutor.OwnerDNS)
		}
	}
	for _, item := range rollback.NFTables {
		if ownedRollbackMetadata(item.Owner, netexecutor.OwnerFirewall) {
			add("nftables", nftRollbackTarget(item), netexecutor.OwnerFirewall)
		}
	}
	return out
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

func ambiguousDesiredRouteShortTargetMismatches(desired []txstate.RoutePlan, rollback []txstate.RouteRollback) []string {
	desiredByShort := map[string]map[string]int{}
	for _, item := range desired {
		if item.Operation != "add" || !ownedRollbackMetadata(item.Owner, netexecutor.OwnerRoute) {
			continue
		}
		rollbackItem := txstate.RouteRollback{Table: item.Table, CIDR: item.CIDR, Via: item.Via, Dev: item.Dev, Owner: item.Owner}
		short := routeRollbackTarget(rollbackItem)
		full := routeRollbackKey(rollbackItem)
		if desiredByShort[short] == nil {
			desiredByShort[short] = map[string]int{}
		}
		desiredByShort[short][full]++
	}
	rollbackByShort := map[string]int{}
	for _, item := range rollback {
		if !ownedRollbackMetadata(item.Owner, netexecutor.OwnerRoute) {
			continue
		}
		rollbackByShort[routeRollbackTarget(item)]++
	}
	var reasons []string
	for short, count := range rollbackByShort {
		fulls := desiredByShort[short]
		if len(fulls) != 1 {
			reasons = append(reasons, "route desired plan has ambiguous full tuples for applied route target "+short)
			continue
		}
		for _, desiredCount := range fulls {
			if desiredCount != count {
				reasons = append(reasons, "route desired plan cardinality does not match applied route target "+short)
			}
		}
	}
	return reasons
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

func stringCounterSubset(subset, superset map[string]int) bool {
	for key, count := range subset {
		if superset[key] < count {
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
