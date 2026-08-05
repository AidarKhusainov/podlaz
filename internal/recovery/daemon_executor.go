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

// DaemonCleanupExecutor is the privileged daemon recovery implementation.
// It intentionally rejects ambiguous rollback metadata before mutating state.
// In particular, it never removes the runtime root and never signals a PID from
// stale transaction metadata because PID reuse makes that unsafe.
type DaemonCleanupExecutor struct {
	Runner     CommandRunner
	RuntimeDir string
}

func (e DaemonCleanupExecutor) Cleanup(ctx context.Context, candidate Candidate) CleanupResult {
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

func (e DaemonCleanupExecutor) CleanupMany(ctx context.Context, candidate Candidate) []CleanupResult {
	if strings.TrimSpace(candidate.Kind) == "" {
		return []CleanupResult{skipped(candidate, "missing recovery candidate kind")}
	}
	e = e.withDefaults()
	osExec := OSCleanupExecutor{Runner: e.Runner, RuntimeDir: e.RuntimeDir}

	switch candidate.Kind {
	case "tun-interface":
		return []CleanupResult{osExec.cleanupTUNInterface(ctx, candidate)}
	case managedDNSCandidateKind:
		return []CleanupResult{osExec.cleanupManagedResolvedLink(ctx, candidate)}
	case "nftables-table":
		return []CleanupResult{osExec.cleanupNFTablesTable(ctx, candidate)}
	case "transaction-state":
		return e.cleanupTransactionState(ctx, candidate, osExec)
	case "generated-runtime-configs":
		return []CleanupResult{osExec.cleanupGeneratedRuntimeConfigs(candidate)}
	case "runtime-directory":
		return []CleanupResult{skipped(candidate, "runtime root cleanup is intentionally unsupported")}
	default:
		return []CleanupResult{skipped(candidate, "unsupported recovery candidate kind")}
	}
}

func (e DaemonCleanupExecutor) withDefaults() DaemonCleanupExecutor {
	if e.Runner == nil {
		e.Runner = OSRunner{}
	}
	if strings.TrimSpace(e.RuntimeDir) == "" {
		e.RuntimeDir = defaultRuntimeDir
	}
	e.RuntimeDir = filepath.Clean(e.RuntimeDir)
	return e
}

func (e DaemonCleanupExecutor) cleanupTransactionState(ctx context.Context, candidate Candidate, osExec OSCleanupExecutor) []CleanupResult {
	if candidate.Transaction == nil {
		return []CleanupResult{skipped(candidate, "missing transaction summary")}
	}
	path := filepath.Clean(candidate.Transaction.Path)
	if !sameCleanPath(path, candidate.Target) || !isTransactionPath(e.RuntimeDir, path) {
		return []CleanupResult{skipped(candidate, "transaction path is outside podlaz runtime state")}
	}
	tx, err := txstate.LoadTransactionFile(path)
	if err != nil {
		return []CleanupResult{failed(candidate, fmt.Errorf("load transaction state: %w", err))}
	}
	if !tx.RequiresRecovery() {
		return []CleanupResult{recovered(candidate)}
	}

	rollback := recoveryRollbackMetadata(tx)
	applyingOwnershipGaps := applyingDesiredOwnershipGaps(tx)
	processResults := e.rollbackChildProcessResults(rollback.ChildProcesses)
	childAbsenceProven := trackedChildAbsenceProven(rollback.ChildProcesses, processResults)
	results := make([]CleanupResult, 0)
	if gateResult, ok := e.rollbackLinkIdentityGate(ctx, osExec, rollback); !ok {
		results = append(results, gateResult)
		results = append(results, e.skipLinkScopedRollbackResults(rollback)...)
		results = append(results, processResults...)
		results = append(results, e.preserveGeneratedConfigResults(rollback.GeneratedConfigs)...)
		results = append(results, e.inspectUnrecordedDesiredMainState(ctx, tx)...)
		results = append(results, skipped(candidate, "transaction cleanup skipped ambiguous link identity; transaction state was preserved"))
		return results
	}
	results = append(results, e.rollbackNFTablesResults(ctx, osExec, rollback.NFTables)...)
	results = append(results, e.rollbackDNSResults(ctx, osExec, rollback.DNS)...)
	results = append(results, e.rollbackPolicyRuleResults(ctx, osExec, rollback.PolicyRules)...)
	results = append(results, e.rollbackRouteResults(ctx, osExec, rollback.Routes)...)
	results = append(results, e.rollbackTUNAddressResults(ctx, rollback.TUNAddresses, childAbsenceProven)...)
	results = append(results, e.rollbackTUNResults(ctx, osExec, rollback.TUN)...)
	results = append(results, processResults...)
	if len(applyingOwnershipGaps) > 0 {
		results = append(results, skipped(Candidate{
			Kind:        "transaction-ownership",
			Description: "applying ownership gap",
			Target:      path,
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
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		results = append(results, failed(candidate, fmt.Errorf("remove transaction state %s: %w", path, err)))
		return results
	}
	results = append(results, recovered(candidate))
	return results
}

type rollbackLinkIdentity struct {
	Name  string
	Index int
	Kind  string
}

func (e DaemonCleanupExecutor) rollbackLinkIdentityGate(ctx context.Context, osExec OSCleanupExecutor, rollback txstate.RollbackMetadata) (CleanupResult, bool) {
	if !rollbackRequiresLinkIdentity(rollback) {
		return CleanupResult{}, true
	}
	candidate := Candidate{Kind: "tun-link-identity", Description: "TUN link identity", Target: managedInterface}
	expected, ok := exactRollbackTUNAddressIdentity(rollback.TUNAddresses)
	if !ok {
		return skipped(candidate, "missing exact transaction-bound TUN address identity for link-scoped rollback"), false
	}
	result, err := osExec.runResult(ctx, "ip", "-details", "-o", "link", "show", "dev", managedInterface)
	if err != nil || !commandSucceeded(result, err) {
		if resourceMissing(result) {
			return skipped(candidate, "transaction-bound TUN link is absent; link-scoped rollback was not attempted"), false
		}
		return failed(candidate, fmt.Errorf("inspect TUN link identity before rollback: %s", commandFailureMessage(result, err))), false
	}
	current, ok := parseRollbackLinkIdentity(result.Stdout)
	if !ok || current.Name != managedInterface || current.Index != expected.LinkIndex || current.Kind != expected.LinkKind {
		return skipped(candidate, fmt.Sprintf("current link identity does not match transaction-bound identity: expected name=%s ifindex=%d kind=%s", managedInterface, expected.LinkIndex, expected.LinkKind)), false
	}
	return CleanupResult{}, true
}

func rollbackRequiresLinkIdentity(rollback txstate.RollbackMetadata) bool {
	return len(rollback.NFTables) > 0 || len(rollback.DNS) > 0 || len(rollback.PolicyRules) > 0 || len(rollback.Routes) > 0 || len(rollback.TUNAddresses) > 0 || len(rollback.TUN) > 0
}

func exactRollbackTUNAddressIdentity(addresses []txstate.TUNAddressRollback) (txstate.TUNAddressRollback, bool) {
	var out txstate.TUNAddressRollback
	matches := 0
	for _, address := range addresses {
		if !ownedRollbackMetadata(address.Owner, netexecutor.OwnerTunAddress) || address.InterfaceName != managedInterface || address.LinkIndex <= 0 || address.LinkKind != "tun" {
			continue
		}
		matches++
		out = address
	}
	return out, matches == 1
}

func parseRollbackLinkIdentity(output string) (rollbackLinkIdentity, bool) {
	line := firstNonEmptyLine(output)
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return rollbackLinkIdentity{}, false
	}
	index, err := strconv.Atoi(strings.TrimSuffix(fields[0], ":"))
	if err != nil || index <= 0 {
		return rollbackLinkIdentity{}, false
	}
	name := strings.TrimSuffix(fields[1], ":")
	name = strings.Split(name, "@")[0]
	kind := ""
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "type" && fields[i+1] == "tun" {
			kind = "tun"
			break
		}
	}
	return rollbackLinkIdentity{Name: name, Index: index, Kind: kind}, name != "" && kind != ""
}

func (e DaemonCleanupExecutor) skipLinkScopedRollbackResults(rollback txstate.RollbackMetadata) []CleanupResult {
	var results []CleanupResult
	for _, entry := range rollback.NFTables {
		results = append(results, skipped(Candidate{Kind: "nftables-table", Description: "nftables table", Target: entry.Family + " " + entry.Table}, "link identity was not proven before rollback"))
	}
	for _, entry := range rollback.DNS {
		results = append(results, skipped(Candidate{Kind: "dns", Description: "DNS link state", Target: entry.Link}, "link identity was not proven before rollback"))
	}
	for _, rule := range rollback.PolicyRules {
		results = append(results, skipped(Candidate{Kind: "policy-rule", Description: "policy rule", Target: fmt.Sprintf("priority %d table %s", rule.Priority, rule.Table)}, "link identity was not proven before rollback"))
	}
	for _, route := range rollback.Routes {
		results = append(results, skipped(Candidate{Kind: "route", Description: "route", Target: fmt.Sprintf("%s table %s", route.CIDR, route.Table)}, "link identity was not proven before rollback"))
	}
	for _, address := range rollback.TUNAddresses {
		results = append(results, skipped(Candidate{Kind: "tun-address", Description: "TUN address", Target: address.InterfaceName + " " + address.CIDR}, "link identity was not proven before rollback"))
	}
	for _, tun := range rollback.TUN {
		results = append(results, skipped(Candidate{Kind: "tun-interface", Description: "TUN interface", Target: tun.InterfaceName}, "link identity was not proven before rollback"))
	}
	return results
}

func applyingDesiredOwnershipGaps(tx txstate.Transaction) []string {
	if tx.State != txstate.TransactionApplying {
		return nil
	}
	var gaps []string
	desired := tx.DesiredPlan
	rollback := tx.Rollback

	if desiredTunAddressIntent(desired.TUNAddress) && len(rollback.TUNAddresses) == 0 && !boundApplyingTunAddressCandidate(desired.TUNAddress) {
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

func desiredTunAddressIntent(address txstate.TUNAddressDesiredState) bool {
	return address.Owner == netexecutor.OwnerTunAddress &&
		address.InterfaceName == managedInterface &&
		strings.TrimSpace(address.CIDR) != ""
}

func boundApplyingTunAddressCandidate(address txstate.TUNAddressDesiredState) bool {
	return desiredTunAddressIntent(address) &&
		address.LinkIndex > 0 &&
		address.LinkKind == "tun" &&
		address.AppearedAfterCore &&
		strings.TrimSpace(address.CIDR) == planner.DefaultTunIPv4CIDR
}

func desiredRouteIntentCount(routes []txstate.RoutePlan) int {
	count := 0
	for _, route := range routes {
		if route.Operation == "add" && ownedRollbackMetadata(route.Owner, netexecutor.OwnerRoute) {
			count++
		}
	}
	return count
}

func desiredPolicyRuleIntentCount(steps []txstate.PlannedStep) int {
	count := 0
	for _, step := range steps {
		if step.Kind == "policy-rule" && ownedRollbackMetadata(step.Owner, netexecutor.OwnerPolicyRule) {
			count++
		}
	}
	return count
}

func desiredDNSIntent(dns txstate.DNSPlan) bool {
	return ownedRollbackMetadata(dns.Owner, netexecutor.OwnerDNS) && strings.TrimSpace(dns.Link) != ""
}

func desiredNFTIntent(nft txstate.NFTPlan) bool {
	return ownedRollbackMetadata(nft.Owner, netexecutor.OwnerFirewall) &&
		strings.TrimSpace(nft.Family) != "" && strings.TrimSpace(nft.Table) != ""
}

func (e DaemonCleanupExecutor) rollbackChildProcessResults(processes []txstate.ChildProcessRollback) []CleanupResult {
	results := make([]CleanupResult, 0, len(processes))
	for _, proc := range processes {
		candidate := Candidate{Kind: "child-process", Description: "child process", Target: fmt.Sprintf("%s pid %d", proc.Label, proc.PID)}
		if proc.Owner != txstate.TransactionOwner {
			results = append(results, skipped(candidate, "non-podlaz child process metadata"))
			continue
		}
		if proc.PID <= 1 {
			results = append(results, skipped(candidate, "no live process pid recorded"))
			continue
		}
		_, err := os.Stat(fmt.Sprintf("/proc/%d", proc.PID))
		switch {
		case errors.Is(err, os.ErrNotExist):
			results = append(results, recoveredWithMessage(candidate, "recorded child process is already absent"))
		case err != nil:
			results = append(results, failed(candidate, fmt.Errorf("inspect stale child process pid %d: %w", proc.PID, err)))
		default:
			results = append(results, skipped(candidate, "process identity cannot be verified from stale metadata"))
		}
	}
	return results
}

func (e DaemonCleanupExecutor) rollbackNFTablesResults(ctx context.Context, osExec OSCleanupExecutor, entries []txstate.NFTablesRollback) []CleanupResult {
	seen := make(map[string]struct{})
	results := make([]CleanupResult, 0, len(entries))
	for _, entry := range entries {
		candidate := Candidate{Kind: "nftables-table", Description: "nftables table", Target: entry.Family + " " + entry.Table}
		if !ownedRollbackMetadata(entry.Owner, netexecutor.OwnerFirewall) || !isManagedNFTTarget(entry.Family, entry.Table) {
			results = append(results, skipped(candidate, "ambiguous or non-podlaz nftables target"))
			continue
		}
		key := entry.Family + " " + entry.Table
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		rollback := entry
		rollback.Owner = txstate.TransactionOwner
		if err := osExec.rollbackNFTables(ctx, []txstate.NFTablesRollback{rollback}); err != nil {
			results = append(results, failed(candidate, err))
			continue
		}
		results = append(results, recovered(candidate))
	}
	return results
}

func (e DaemonCleanupExecutor) rollbackDNSResults(ctx context.Context, osExec OSCleanupExecutor, entries []txstate.DNSRollback) []CleanupResult {
	results := make([]CleanupResult, 0, len(entries))
	for _, dns := range entries {
		candidate := Candidate{Kind: "dns", Description: "DNS link state", Target: dns.Link}
		if !ownedRollbackMetadata(dns.Owner, netexecutor.OwnerDNS) || dns.Link != managedInterface || !systemdResolvedBackend(dns.Backend) {
			results = append(results, skipped(candidate, "ambiguous or non-podlaz DNS rollback target"))
			continue
		}
		rollback := dns
		rollback.Owner = txstate.TransactionOwner
		rollback.Backend = normalizedSystemdResolvedBackend
		if err := osExec.rollbackDNS(ctx, rollback); err != nil {
			results = append(results, failed(candidate, err))
			continue
		}
		results = append(results, recovered(candidate))
	}
	return results
}

func (e DaemonCleanupExecutor) rollbackPolicyRuleResults(ctx context.Context, osExec OSCleanupExecutor, rules []txstate.PolicyRuleRollback) []CleanupResult {
	results := make([]CleanupResult, 0, len(rules))
	for _, rule := range rules {
		candidate := Candidate{Kind: "policy-rule", Description: "policy rule", Target: fmt.Sprintf("priority %d table %s", rule.Priority, rule.Table)}
		if !ownedRollbackMetadata(rule.Owner, netexecutor.OwnerPolicyRule) {
			results = append(results, skipped(candidate, "non-podlaz policy rule metadata"))
			continue
		}
		if safeMainServerBypassPolicyRule(rule) {
			if err := rollbackMainServerBypassPolicyRule(ctx, osExec, rule); err != nil {
				results = append(results, failed(candidate, err))
				continue
			}
			results = append(results, recovered(candidate))
			continue
		}
		if _, ok := managedTableToken(rule.Table); !ok {
			results = append(results, skipped(candidate, "ambiguous or non-podlaz policy rule table"))
			continue
		}
		rollback := rule
		rollback.Owner = txstate.TransactionOwner
		if err := osExec.rollbackPolicyRule(ctx, rollback); err != nil {
			results = append(results, failed(candidate, err))
			continue
		}
		results = append(results, recovered(candidate))
	}
	return results
}

func (e DaemonCleanupExecutor) rollbackRouteResults(ctx context.Context, osExec OSCleanupExecutor, routes []txstate.RouteRollback) []CleanupResult {
	results := make([]CleanupResult, 0, len(routes))
	for _, route := range routes {
		candidate := Candidate{Kind: "route", Description: "route", Target: fmt.Sprintf("%s table %s", route.CIDR, route.Table)}
		if !ownedRollbackMetadata(route.Owner, netexecutor.OwnerRoute) {
			results = append(results, skipped(candidate, "non-podlaz route metadata"))
			continue
		}
		if safeMainServerBypassRoute(route) {
			if err := rollbackMainServerBypassRoute(ctx, osExec, route); err != nil {
				results = append(results, failed(candidate, err))
				continue
			}
			results = append(results, recovered(candidate))
			continue
		}
		if strings.TrimSpace(route.Table) == "main" {
			results = append(results, skipped(candidate, "ambiguous or non-podlaz main-table route"))
			continue
		}
		if _, ok := managedTableToken(route.Table); !ok {
			results = append(results, skipped(candidate, "ambiguous or non-podlaz route table"))
			continue
		}
		if strings.TrimSpace(route.Dev) != "" && route.Dev != managedInterface {
			results = append(results, skipped(candidate, "ambiguous or non-podlaz route device"))
			continue
		}
		rollback := route
		rollback.Owner = txstate.TransactionOwner
		if err := osExec.rollbackRoute(ctx, rollback); err != nil {
			results = append(results, failed(candidate, err))
			continue
		}
		results = append(results, recovered(candidate))
	}
	return results
}

func (e DaemonCleanupExecutor) rollbackTUNResults(ctx context.Context, osExec OSCleanupExecutor, entries []txstate.TUNRollback) []CleanupResult {
	results := make([]CleanupResult, 0, len(entries))
	for _, tun := range entries {
		candidate := Candidate{Kind: "tun-interface", Description: "TUN interface", Target: tun.InterfaceName}
		if !ownedRollbackMetadata(tun.Owner, netexecutor.OwnerTunDevice) || tun.InterfaceName != managedInterface {
			results = append(results, skipped(candidate, "ambiguous or non-podlaz TUN target"))
			continue
		}
		rollback := tun
		rollback.Owner = txstate.TransactionOwner
		if err := osExec.rollbackTUN(ctx, rollback); err != nil {
			results = append(results, failed(candidate, err))
			continue
		}
		results = append(results, recovered(candidate))
	}
	return results
}

func (e DaemonCleanupExecutor) preserveGeneratedConfigResults(configs []txstate.GeneratedConfigRollback) []CleanupResult {
	results := make([]CleanupResult, 0, len(configs))
	for _, config := range configs {
		candidate := Candidate{Kind: "generated-runtime-config", Description: "generated runtime config", Target: config.Path}
		if config.Owner != txstate.TransactionOwner {
			results = append(results, skipped(candidate, "non-podlaz generated config metadata"))
			continue
		}
		if !isUnderDir(filepath.Join(e.RuntimeDir, generatedDirName), filepath.Clean(config.Path)) {
			results = append(results, skipped(candidate, "generated config path is outside podlaz runtime state"))
			continue
		}
		results = append(results, skipped(candidate, "process absence is unproven; generated config was preserved"))
	}
	return results
}

func (e DaemonCleanupExecutor) rollbackGeneratedConfigResults(osExec OSCleanupExecutor, configs []txstate.GeneratedConfigRollback) []CleanupResult {
	results := make([]CleanupResult, 0, len(configs))
	for _, config := range configs {
		candidate := Candidate{Kind: "generated-runtime-config", Description: "generated runtime config", Target: config.Path}
		if config.Owner != txstate.TransactionOwner {
			results = append(results, skipped(candidate, "non-podlaz generated config metadata"))
			continue
		}
		if !isUnderDir(filepath.Join(e.RuntimeDir, generatedDirName), filepath.Clean(config.Path)) {
			results = append(results, skipped(candidate, "generated config path is outside podlaz runtime state"))
			continue
		}
		if err := osExec.removeGeneratedConfig(config); err != nil {
			results = append(results, failed(candidate, err))
			continue
		}
		if err := removeEmptyGeneratedRoot(e.RuntimeDir); err != nil {
			results = append(results, failed(candidate, err))
			continue
		}
		results = append(results, recovered(candidate))
	}
	return results
}

func ownedRollbackMetadata(owner, expected string) bool {
	owner = strings.TrimSpace(owner)
	expected = strings.TrimSpace(expected)
	return expected != "" && (owner == expected || owner == txstate.TransactionOwner)
}

func safeMainServerBypassPolicyRule(rule txstate.PolicyRuleRollback) bool {
	if rule.Priority != planner.ServerRulePriority || strings.TrimSpace(rule.Table) != planner.MainRoutingTable {
		return false
	}
	if strings.TrimSpace(rule.From) != "" || strings.TrimSpace(rule.Mark) != "" {
		return false
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(rule.To))
	if err != nil {
		return false
	}
	return prefix.Addr().Is4() && prefix.Bits() == 32
}

func rollbackMainServerBypassPolicyRule(ctx context.Context, osExec OSCleanupExecutor, rule txstate.PolicyRuleRollback) error {
	if !ownedRollbackMetadata(rule.Owner, netexecutor.OwnerPolicyRule) || !safeMainServerBypassPolicyRule(rule) {
		return fmt.Errorf("refuse to rollback ambiguous main-table policy rule priority %d", rule.Priority)
	}
	to := strings.TrimSpace(rule.To)
	if err := osExec.run(ctx, "ip", "-4", "rule", "del", "priority", fmt.Sprint(rule.Priority), "to", to, "lookup", planner.MainRoutingTable); err != nil && !commandErrorIsMissing(err) {
		return fmt.Errorf("delete main-table server bypass policy rule priority %d to %s: %w", rule.Priority, to, err)
	}
	return nil
}

func safeMainServerBypassRoute(route txstate.RouteRollback) bool {
	if strings.TrimSpace(route.Table) != planner.MainRoutingTable {
		return false
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(route.CIDR))
	if err != nil {
		return false
	}
	return prefix.Addr().Is4() && prefix.Bits() == 32 && strings.TrimSpace(route.Via) != "" && strings.TrimSpace(route.Dev) != ""
}

func rollbackMainServerBypassRoute(ctx context.Context, osExec OSCleanupExecutor, route txstate.RouteRollback) error {
	if !ownedRollbackMetadata(route.Owner, netexecutor.OwnerRoute) || !safeMainServerBypassRoute(route) {
		return fmt.Errorf("refuse to rollback ambiguous main-table route %s", route.CIDR)
	}
	cidr := strings.TrimSpace(route.CIDR)
	via := strings.TrimSpace(route.Via)
	dev := strings.TrimSpace(route.Dev)
	if err := osExec.run(ctx, "ip", "-4", "route", "del", cidr, "via", via, "dev", dev, "table", planner.MainRoutingTable); err != nil && !commandErrorIsMissing(err) {
		return fmt.Errorf("delete main-table server bypass route %s via %s dev %s: %w", cidr, via, dev, err)
	}
	return nil
}

func hasFailedCleanup(results []CleanupResult) bool {
	for _, result := range results {
		if result.Status == "failed" {
			return true
		}
	}
	return false
}

func hasSkippedCleanup(results []CleanupResult) bool {
	for _, result := range results {
		if result.Status == "skipped" {
			return true
		}
	}
	return false
}
