package recovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		results = append(results, failed(candidate, fmt.Errorf("remove transaction state %s: %w", path, err)))
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
		if entry.Owner != txstate.TransactionOwner || !isManagedNFTTarget(entry.Family, entry.Table) {
			results = append(results, skipped(candidate, "ambiguous or non-podlaz nftables target"))
			continue
		}
		key := entry.Family + " " + entry.Table
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := osExec.rollbackNFTables(ctx, []txstate.NFTablesRollback{entry}); err != nil {
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
		if dns.Owner != txstate.TransactionOwner || dns.Link != managedInterface || (dns.Backend != "" && dns.Backend != "systemd-resolved") {
			results = append(results, skipped(candidate, "ambiguous or non-podlaz DNS rollback target"))
			continue
		}
		if err := osExec.rollbackDNS(ctx, dns); err != nil {
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
		if rule.Owner != txstate.TransactionOwner {
			results = append(results, skipped(candidate, "non-podlaz policy rule metadata"))
			continue
		}
		if _, ok := managedTableToken(rule.Table); !ok {
			results = append(results, skipped(candidate, "ambiguous or non-podlaz policy rule table"))
			continue
		}
		if err := osExec.rollbackPolicyRule(ctx, rule); err != nil {
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
		if route.Owner != txstate.TransactionOwner {
			results = append(results, skipped(candidate, "non-podlaz route metadata"))
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
		if err := osExec.rollbackRoute(ctx, route); err != nil {
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
		if tun.Owner != txstate.TransactionOwner || tun.InterfaceName != managedInterface {
			results = append(results, skipped(candidate, "ambiguous or non-podlaz TUN target"))
			continue
		}
		if err := osExec.rollbackTUN(ctx, tun); err != nil {
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
