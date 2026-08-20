package recovery

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// NetworkSessionCleanupExecutor extends legacy cleanup with exact resource
// identities persisted by collision-aware Network Sessions. Allocation range
// membership is never sufficient delete authority: transaction desired,
// applied, and rollback tuples must first pass the existing consistency proof.
type NetworkSessionCleanupExecutor struct {
	Runner     CommandRunner
	RuntimeDir string
}

func (e NetworkSessionCleanupExecutor) Cleanup(ctx context.Context, candidate Candidate) CleanupResult {
	results := e.CleanupMany(ctx, candidate)
	if len(results) == 0 {
		return skipped(candidate, "no cleanup action produced a result")
	}
	if len(results) == 1 {
		return results[0]
	}
	for _, result := range results {
		if result.Status == "failed" {
			return failed(candidate, errors.New("transaction cleanup completed with failures"))
		}
	}
	for _, result := range results {
		if result.Status == "skipped" {
			return skipped(candidate, "transaction cleanup skipped at least one resource")
		}
	}
	return recovered(candidate)
}

func (e NetworkSessionCleanupExecutor) CleanupMany(ctx context.Context, candidate Candidate) []CleanupResult {
	legacy := DaemonCleanupExecutor{Runner: e.Runner, RuntimeDir: e.RuntimeDir}.withDefaults()
	if candidate.Kind != "transaction-state" || candidate.Transaction == nil {
		return legacy.CleanupMany(ctx, candidate)
	}

	path := filepath.Clean(candidate.Transaction.Path)
	if !sameCleanPath(path, candidate.Target) || !isTransactionPath(legacy.RuntimeDir, path) {
		return legacy.CleanupMany(ctx, candidate)
	}
	tx, err := txstate.LoadTransactionFile(path)
	if err != nil || !tx.RequiresRecovery() {
		return legacy.CleanupMany(ctx, candidate)
	}
	allocation, ok := exactPersistedNetworkSessionAllocation(tx)
	if !ok {
		return legacy.CleanupMany(ctx, candidate)
	}
	return cleanupAllocatedNetworkSessionTransaction(ctx, legacy, candidate, tx, allocation)
}

type persistedNetworkSessionAllocation struct {
	TunIPv4CIDR        string
	RoutingTable       string
	ServerRulePriority int
	TunnelRulePriority int
}

func exactPersistedNetworkSessionAllocation(tx txstate.Transaction) (persistedNetworkSessionAllocation, bool) {
	address := tx.DesiredPlan.TUNAddress
	if address.InterfaceName != managedInterface || !ownedRollbackMetadata(address.Owner, netexecutor.OwnerTunAddress) || !planner.IsAllocatedTunIPv4CIDR(address.CIDR) {
		return persistedNetworkSessionAllocation{}, false
	}

	allocation := persistedNetworkSessionAllocation{TunIPv4CIDR: strings.TrimSpace(address.CIDR)}
	routeMatches := 0
	for _, route := range tx.DesiredPlan.Routes {
		if route.Kind != "route" || route.Operation != "add" || !ownedRollbackMetadata(route.Owner, netexecutor.OwnerRoute) {
			continue
		}
		if route.Dev == managedInterface && (route.CIDR == planner.IPv4DefaultRoute || route.CIDR == "0.0.0.0/0") && planner.IsAllocatedTunRoutingTable(route.Table) {
			routeMatches++
			allocation.RoutingTable = strings.TrimSpace(route.Table)
		}
	}
	if routeMatches != 1 {
		return persistedNetworkSessionAllocation{}, false
	}

	serverMatches := 0
	tunnelMatches := 0
	for _, step := range tx.DesiredPlan.Steps {
		if step.Kind != "policy-rule" || !ownedRollbackMetadata(step.Owner, netexecutor.OwnerPolicyRule) {
			continue
		}
		priority, selector, table, ok := parseNetworkSessionPolicyRuleTarget(step.Target)
		if !ok {
			return persistedNetworkSessionAllocation{}, false
		}
		switch {
		case table == planner.MainRoutingTable && strings.HasPrefix(selector, "to "):
			prefix, err := netip.ParsePrefix(strings.TrimPrefix(selector, "to "))
			if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 32 {
				return persistedNetworkSessionAllocation{}, false
			}
			serverMatches++
			allocation.ServerRulePriority = priority
		case table == allocation.RoutingTable && selector == planner.IPv4DefaultSelector:
			tunnelMatches++
			allocation.TunnelRulePriority = priority
		}
	}
	if serverMatches != 1 || tunnelMatches != 1 || allocation.ServerRulePriority <= 0 || allocation.TunnelRulePriority <= 0 || allocation.ServerRulePriority >= allocation.TunnelRulePriority {
		return persistedNetworkSessionAllocation{}, false
	}
	return allocation, true
}

func parseNetworkSessionPolicyRuleTarget(target string) (priority int, selector, table string, ok bool) {
	fields := strings.Fields(strings.TrimSpace(target))
	if len(fields) < 5 || fields[0] != "priority" {
		return 0, "", "", false
	}
	priority, err := strconv.Atoi(fields[1])
	if err != nil || priority <= 0 || priority >= 32766 {
		return 0, "", "", false
	}
	lookup := -1
	for i := 2; i+1 < len(fields); i++ {
		if fields[i] == "lookup" {
			lookup = i
			break
		}
	}
	if lookup <= 2 || lookup+1 != len(fields)-1 {
		return 0, "", "", false
	}
	selector = strings.Join(fields[2:lookup], " ")
	table = fields[lookup+1]
	return priority, selector, table, selector != "" && table != ""
}

func cleanupAllocatedNetworkSessionTransaction(ctx context.Context, e DaemonCleanupExecutor, candidate Candidate, tx txstate.Transaction, allocation persistedNetworkSessionAllocation) []CleanupResult {
	osExec := OSCleanupExecutor{Runner: e.Runner, RuntimeDir: e.RuntimeDir}.withDefaults()
	rollback := networkSessionRecoveryRollbackMetadata(tx, allocation)
	applyingOwnershipGaps := networkSessionApplyingOwnershipGaps(tx, allocation)
	processResults := e.rollbackChildProcessResults(rollback.ChildProcesses)
	childAbsenceProven := trackedChildAbsenceProven(rollback.ChildProcesses, processResults)
	results := make([]CleanupResult, 0)

	gateResult, gateDecision := networkSessionRollbackLinkIdentityGate(ctx, e, osExec, rollback, tx.AppliedSteps, childAbsenceProven)
	switch gateDecision {
	case rollbackLinkBlocked:
		results = append(results, e.rollbackNFTablesResults(ctx, osExec, rollback.NFTables)...)
		results = append(results, rollbackNetworkSessionPolicyRules(ctx, osExec, rollback.PolicyRules, allocation)...)
		results = append(results, rollbackNetworkSessionIndependentRoutes(ctx, osExec, rollback.Routes, allocation)...)
		results = append(results, gateResult)
		results = append(results, e.failLinkScopedRollbackResults(rollback)...)
		results = append(results, processResults...)
		results = append(results, e.preserveGeneratedConfigResults(rollback.GeneratedConfigs)...)
		results = append(results, e.inspectUnrecordedDesiredMainState(ctx, tx)...)
		results = append(results, failed(candidate, errors.New("transaction cleanup failed link identity proof; transaction state was preserved")))
		return results
	case rollbackLinkAbsentChildAbsent:
		results = append(results, e.rollbackNFTablesResults(ctx, osExec, rollback.NFTables)...)
		results = append(results, rollbackNetworkSessionPolicyRules(ctx, osExec, rollback.PolicyRules, allocation)...)
		results = append(results, rollbackNetworkSessionIndependentRoutes(ctx, osExec, rollback.Routes, allocation)...)
		results = append(results, missingNetworkSessionLinkRoutes(rollback.Routes, allocation)...)
		results = append(results, gateResult)
		results = append(results, e.missingLinkScopedRollbackResults(rollback)...)
		results = append(results, processResults...)
	default:
		results = append(results, e.rollbackNFTablesResults(ctx, osExec, rollback.NFTables)...)
		results = append(results, e.rollbackDNSResults(ctx, osExec, rollback.DNS)...)
		results = append(results, rollbackNetworkSessionPolicyRules(ctx, osExec, rollback.PolicyRules, allocation)...)
		results = append(results, rollbackNetworkSessionRoutes(ctx, osExec, rollback.Routes, allocation)...)
		results = append(results, e.rollbackTUNAddressResults(ctx, rollback.TUNAddresses, childAbsenceProven)...)
		results = append(results, e.rollbackTUNResults(ctx, osExec, rollback.TUN)...)
		results = append(results, processResults...)
	}

	if len(applyingOwnershipGaps) > 0 {
		results = append(results, skipped(Candidate{
			Kind:        "transaction-ownership",
			Description: "applying ownership gap",
			Target:      candidate.Target,
		}, "applying transaction has no durable ownership proof for "+strings.Join(applyingOwnershipGaps, ", ")))
	}
	if len(applyingOwnershipGaps) > 0 || hasFailedCleanup(processResults) || hasSkippedCleanup(processResults) {
		results = append(results, e.preserveGeneratedConfigResults(rollback.GeneratedConfigs)...)
	} else {
		results = append(results, e.rollbackGeneratedConfigResults(osExec, rollback.GeneratedConfigs)...)
	}
	results = append(results, e.inspectUnrecordedDesiredMainState(ctx, tx)...)

	if hasFailedCleanup(results) {
		results = append(results, failed(candidate, errors.New("transaction cleanup completed with failures; transaction state was preserved")))
		return results
	}
	if hasSkippedCleanup(results) {
		results = append(results, skipped(candidate, "transaction cleanup skipped ambiguous resources; transaction state was preserved"))
		return results
	}
	if err := os.Remove(filepath.Clean(candidate.Transaction.Path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		results = append(results, failed(candidate, fmt.Errorf("remove transaction state %s: %w", candidate.Transaction.Path, err)))
		return results
	}
	results = append(results, recovered(candidate))
	return results
}

func networkSessionRecoveryRollbackMetadata(tx txstate.Transaction, allocation persistedNetworkSessionAllocation) txstate.RollbackMetadata {
	rollback := tx.Rollback
	if reasons := rollbackOwnershipConsistencyReasons(tx, rollback); len(reasons) > 0 {
		return mutationFreeAmbiguousRollback(rollback, reasons)
	}
	if len(rollback.TUNAddresses) == 0 && tx.State == txstate.TransactionApplying {
		address := tx.DesiredPlan.TUNAddress
		if networkSessionBoundApplyingTunAddressCandidate(address, allocation) {
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

func networkSessionApplyingOwnershipGaps(tx txstate.Transaction, allocation persistedNetworkSessionAllocation) []string {
	if tx.State != txstate.TransactionApplying {
		return nil
	}
	var gaps []string
	desired := tx.DesiredPlan
	rollback := tx.Rollback
	if desiredTunAddressIntent(desired.TUNAddress) && len(rollback.TUNAddresses) == 0 && !networkSessionBoundApplyingTunAddressCandidate(desired.TUNAddress, allocation) {
		gaps = append(gaps, "TUN address")
	}
	if desiredRouteIntentCount(desired.Routes) > len(rollback.Routes) {
		gaps = append(gaps, "routes")
	}
	if desiredPolicyRuleIntentCount(desired.Steps) > len(rollback.PolicyRules) {
		gaps = append(gaps, "policy rules")
	}
	if desiredDNSIntent(desired.DNS) && len(rollback.DNS) == 0 {
		gaps = append(gaps, "DNS")
	}
	if desiredNFTIntent(desired.NFT) && len(rollback.NFTables) == 0 {
		gaps = append(gaps, "nftables")
	}
	return gaps
}

func networkSessionBoundApplyingTunAddressCandidate(address txstate.TUNAddressDesiredState, allocation persistedNetworkSessionAllocation) bool {
	return desiredTunAddressIntent(address) &&
		address.LinkIndex > 0 &&
		address.LinkKind == "tun" &&
		address.AppearedAfterCore &&
		strings.TrimSpace(address.CIDR) == allocation.TunIPv4CIDR &&
		planner.IsAllocatedTunIPv4CIDR(address.CIDR)
}

func networkSessionRollbackLinkIdentityGate(ctx context.Context, e DaemonCleanupExecutor, osExec OSCleanupExecutor, rollback txstate.RollbackMetadata, applied []txstate.AppliedStep, childAbsenceProven bool) (CleanupResult, rollbackLinkDecision) {
	if !rollbackRequiresLinkIdentity(rollback) {
		return CleanupResult{}, rollbackLinkMatched
	}
	candidate := Candidate{Kind: "tun-link-identity", Description: "TUN link identity", Target: managedInterface}
	expected, ok := exactNetworkSessionRollbackLinkIdentity(rollback, applied)
	if !ok {
		return failed(candidate, errors.New("missing exact transaction-bound TUN address or creation identity for link-scoped rollback")), rollbackLinkBlocked
	}
	result, err := osExec.runResult(ctx, "ip", "-details", "-o", "link", "show", "dev", managedInterface)
	if err != nil || !commandSucceeded(result, err) {
		if resourceMissing(result) {
			if childAbsenceProven {
				return recoveredWithMessage(candidate, "transaction-bound TUN link and tracked child are absent"), rollbackLinkAbsentChildAbsent
			}
			return failed(candidate, errors.New("transaction-bound TUN link is absent but tracked child absence is unproven")), rollbackLinkBlocked
		}
		return failed(candidate, fmt.Errorf("inspect TUN link identity before rollback: %s", commandFailureMessage(result, err))), rollbackLinkBlocked
	}
	current, ok := parseRollbackLinkIdentity(result.Stdout)
	if !ok || current.Name != expected.Name || current.Index != expected.Index || current.Kind != expected.Kind {
		return failed(candidate, fmt.Errorf("current link identity does not match transaction-bound identity: expected name=%s ifindex=%d kind=%s", expected.Name, expected.Index, expected.Kind)), rollbackLinkBlocked
	}
	return CleanupResult{}, rollbackLinkMatched
}

func exactNetworkSessionRollbackLinkIdentity(rollback txstate.RollbackMetadata, applied []txstate.AppliedStep) (rollbackLinkIdentity, bool) {
	if address, ok := exactNetworkSessionRollbackTUNAddressIdentity(rollback.TUNAddresses); ok {
		return rollbackLinkIdentity{Name: managedInterface, Index: address.LinkIndex, Kind: address.LinkKind}, true
	}
	return exactRollbackTUNCreationIdentity(rollback.TUN, applied)
}

func exactNetworkSessionRollbackTUNAddressIdentity(addresses []txstate.TUNAddressRollback) (txstate.TUNAddressRollback, bool) {
	var out txstate.TUNAddressRollback
	matches := 0
	for _, address := range addresses {
		if validateTunAddressRollback(address) != "" {
			continue
		}
		matches++
		out = address
	}
	return out, matches == 1
}

func rollbackNetworkSessionPolicyRules(ctx context.Context, osExec OSCleanupExecutor, rules []txstate.PolicyRuleRollback, allocation persistedNetworkSessionAllocation) []CleanupResult {
	results := make([]CleanupResult, 0, len(rules))
	for _, rule := range rules {
		candidate := Candidate{Kind: "policy-rule", Description: "policy rule", Target: fmt.Sprintf("priority %d table %s", rule.Priority, rule.Table)}
		if !ownedRollbackMetadata(rule.Owner, netexecutor.OwnerPolicyRule) {
			results = append(results, skipped(candidate, "non-podlaz policy rule metadata"))
			continue
		}
		args, ok := exactNetworkSessionPolicyRuleDeleteArgs(rule, allocation)
		if !ok {
			results = append(results, skipped(candidate, "policy rule does not match the exact persisted session allocation"))
			continue
		}
		if err := osExec.run(ctx, "ip", args...); err != nil && !commandErrorIsMissing(err) {
			results = append(results, failed(candidate, err))
			continue
		}
		results = append(results, recovered(candidate))
	}
	return results
}

func exactNetworkSessionPolicyRuleDeleteArgs(rule txstate.PolicyRuleRollback, allocation persistedNetworkSessionAllocation) ([]string, bool) {
	if rule.Priority == allocation.ServerRulePriority && strings.TrimSpace(rule.Table) == planner.MainRoutingTable && strings.TrimSpace(rule.From) == "" && strings.TrimSpace(rule.Mark) == "" {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(rule.To))
		if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 32 {
			return nil, false
		}
		return []string{"-4", "rule", "del", "priority", strconv.Itoa(rule.Priority), "to", prefix.String(), "lookup", planner.MainRoutingTable}, true
	}
	if rule.Priority != allocation.TunnelRulePriority || strings.TrimSpace(rule.Table) != allocation.RoutingTable || strings.TrimSpace(rule.From) != "all" || strings.TrimSpace(rule.To) != "" || strings.TrimSpace(rule.Mark) != "" {
		return nil, false
	}
	return []string{"-4", "rule", "del", "priority", strconv.Itoa(rule.Priority), "from", "all", "lookup", allocation.RoutingTable}, true
}

func rollbackNetworkSessionRoutes(ctx context.Context, osExec OSCleanupExecutor, routes []txstate.RouteRollback, allocation persistedNetworkSessionAllocation) []CleanupResult {
	results := make([]CleanupResult, 0, len(routes))
	for _, route := range routes {
		results = append(results, rollbackNetworkSessionRoute(ctx, osExec, route, allocation))
	}
	return results
}

func rollbackNetworkSessionIndependentRoutes(ctx context.Context, osExec OSCleanupExecutor, routes []txstate.RouteRollback, allocation persistedNetworkSessionAllocation) []CleanupResult {
	results := make([]CleanupResult, 0, len(routes))
	for _, route := range routes {
		if networkSessionLinkDependentRoute(route, allocation) {
			continue
		}
		results = append(results, rollbackNetworkSessionRoute(ctx, osExec, route, allocation))
	}
	return results
}

func missingNetworkSessionLinkRoutes(routes []txstate.RouteRollback, allocation persistedNetworkSessionAllocation) []CleanupResult {
	results := make([]CleanupResult, 0, len(routes))
	for _, route := range routes {
		if !networkSessionLinkDependentRoute(route, allocation) {
			continue
		}
		results = append(results, recoveredWithMessage(Candidate{Kind: "route", Description: "route", Target: fmt.Sprintf("%s table %s", route.CIDR, route.Table)}, "transaction-bound link is absent after tracked child exit; link-dependent route is already absent"))
	}
	return results
}

func networkSessionLinkDependentRoute(route txstate.RouteRollback, allocation persistedNetworkSessionAllocation) bool {
	return strings.TrimSpace(route.Table) == allocation.RoutingTable && strings.TrimSpace(route.Dev) == managedInterface
}

func rollbackNetworkSessionRoute(ctx context.Context, osExec OSCleanupExecutor, route txstate.RouteRollback, allocation persistedNetworkSessionAllocation) CleanupResult {
	candidate := Candidate{Kind: "route", Description: "route", Target: fmt.Sprintf("%s table %s", route.CIDR, route.Table)}
	if !ownedRollbackMetadata(route.Owner, netexecutor.OwnerRoute) {
		return skipped(candidate, "non-podlaz route metadata")
	}
	args, ok := exactNetworkSessionRouteDeleteArgs(route, allocation)
	if !ok {
		return skipped(candidate, "route does not match the exact persisted session allocation")
	}
	if err := osExec.run(ctx, "ip", args...); err != nil && !commandErrorIsMissing(err) {
		return failed(candidate, err)
	}
	return recovered(candidate)
}

func exactNetworkSessionRouteDeleteArgs(route txstate.RouteRollback, allocation persistedNetworkSessionAllocation) ([]string, bool) {
	table := strings.TrimSpace(route.Table)
	cidr := strings.TrimSpace(route.CIDR)
	if table == allocation.RoutingTable && (cidr == planner.IPv4DefaultRoute || cidr == "0.0.0.0/0") && strings.TrimSpace(route.Via) == "" && strings.TrimSpace(route.Dev) == managedInterface {
		return []string{"-4", "route", "del", cidr, "dev", managedInterface, "table", allocation.RoutingTable}, true
	}
	if table != planner.MainRoutingTable || strings.TrimSpace(route.Dev) == "" {
		return nil, false
	}
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 32 {
		return nil, false
	}
	args := []string{"-4", "route", "del", prefix.String()}
	if via := strings.TrimSpace(route.Via); via != "" {
		if addr, err := netip.ParseAddr(via); err != nil || !addr.Is4() {
			return nil, false
		}
		args = append(args, "via", via)
	}
	args = append(args, "dev", strings.TrimSpace(route.Dev), "table", planner.MainRoutingTable)
	return args, true
}
