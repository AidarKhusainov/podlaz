package recovery

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
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
	if !tx.RequiresCleanup() {
		return []CleanupResult{recovered(candidate)}
	}

	results := make([]CleanupResult, 0)
	results = append(results, e.rollbackChildProcessResults(tx.Rollback.ChildProcesses)...)
	results = append(results, e.rollbackNFTablesResults(ctx, osExec, tx.Rollback.NFTables)...)
	results = append(results, e.rollbackDNSResults(ctx, osExec, tx.Rollback.DNS)...)
	results = append(results, e.rollbackPolicyRuleResults(ctx, osExec, tx.Rollback.PolicyRules)...)
	results = append(results, e.rollbackRouteResults(ctx, osExec, tx.Rollback.Routes)...)
	results = append(results, e.rollbackTUNResults(ctx, osExec, tx.Rollback.TUN)...)
	results = append(results, e.rollbackGeneratedConfigResults(osExec, tx.Rollback.GeneratedConfigs)...)

	if hasFailedCleanup(results) {
		results = append(results, failed(candidate, errors.New("transaction cleanup completed with failures; transaction state was preserved")))
		return results
	}
	if hasSkippedCleanup(results) {
		results = append(results, skipped(candidate, "transaction cleanup skipped ambiguous resources; transaction state was preserved"))
		return results
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		results = append(results, failed(candidate, fmt.Errorf("remove transaction state %s: %w", path)))
		return results
	}
	results = append(results, recovered(candidate))
	return results
}

func (e DaemonCleanupExecutor) rollbackChildProcessResults(processes []txstate.ChildProcessRollback) []CleanupResult {
	results := make([]CleanupResult, 0, len(processes))
	for _, proc := range processes {
		candidate := Candidate{Kind: "child-process", Description: "child process", Target: fmt.Sprintf("%s pid %d", proc.Label, proc.PID)}
		if proc.Owner != txstate.TransactionOwner {
			results = append(results, skipped(candidate, "non-podlaz child process metadata"))
			continue
		}
		if proc.PID > 1 {
			results = append(results, skipped(candidate, "process identity cannot be verified from stale metadata"))
			continue
		}
		results = append(results, skipped(candidate, "no live process pid recorded"))
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
		if !ownedRollbackMetadata(dns.Owner, netexecutor.OwnerDNS) || dns.Link != managedInterface || (dns.Backend != "" && dns.Backend != "systemd-resolved") {
			results = append(results, skipped(candidate, "ambiguous or non-podlaz DNS rollback target"))
			continue
		}
		rollback := dns
		rollback.Owner = txstate.TransactionOwner
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
