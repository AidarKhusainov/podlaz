package recovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	txstate "github.com/AidarKhusainov/tunwarden/internal/state"
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
	if strings.TrimSpace(candidate.Kind) == "" {
		return skipped(candidate, "missing recovery candidate kind")
	}
	if e.Runner == nil {
		e.Runner = OSRunner{}
	}
	if strings.TrimSpace(e.RuntimeDir) == "" {
		e.RuntimeDir = defaultRuntimeDir
	}
	e.RuntimeDir = filepath.Clean(e.RuntimeDir)
	osExec := OSCleanupExecutor{Runner: e.Runner, RuntimeDir: e.RuntimeDir}

	switch candidate.Kind {
	case "tun-interface":
		return osExec.cleanupTUNInterface(ctx, candidate)
	case "nftables-table":
		return osExec.cleanupNFTablesTable(ctx, candidate)
	case "transaction-state":
		return e.cleanupTransactionState(ctx, candidate, osExec)
	case "generated-runtime-configs":
		return osExec.cleanupGeneratedRuntimeConfigs(candidate)
	case "runtime-directory":
		return skipped(candidate, "runtime root cleanup is intentionally unsupported")
	default:
		return skipped(candidate, "unsupported recovery candidate kind")
	}
}

func (e DaemonCleanupExecutor) cleanupTransactionState(ctx context.Context, candidate Candidate, osExec OSCleanupExecutor) CleanupResult {
	if candidate.Transaction == nil {
		return skipped(candidate, "missing transaction summary")
	}
	path := filepath.Clean(candidate.Transaction.Path)
	if !sameCleanPath(path, candidate.Target) || !isTransactionPath(e.RuntimeDir, path) {
		return skipped(candidate, "transaction path is outside TunWarden runtime state")
	}
	tx, err := txstate.LoadTransactionFile(path)
	if err != nil {
		return failed(candidate, fmt.Errorf("load transaction state: %w", err))
	}
	if !tx.RequiresCleanup() {
		return recovered(candidate)
	}
	if err := validateSafeRollback(e.RuntimeDir, tx); err != nil {
		return skipped(candidate, err.Error())
	}
	if err := daemonRollbackTransaction(ctx, osExec, tx); err != nil {
		return failed(candidate, err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return failed(candidate, fmt.Errorf("remove transaction state %s: %w", path, err))
	}
	return recovered(candidate)
}

func validateSafeRollback(runtimeDir string, tx txstate.Transaction) error {
	var errs []error
	for _, proc := range tx.Rollback.ChildProcesses {
		if proc.Owner != txstate.TransactionOwner {
			errs = append(errs, fmt.Errorf("ambiguous child process rollback label=%q pid=%d", proc.Label, proc.PID))
			continue
		}
		if proc.PID > 1 {
			errs = append(errs, fmt.Errorf("ambiguous child process pid %d: process identity cannot be verified from stale metadata", proc.PID))
		}
	}
	for _, entry := range tx.Rollback.NFTables {
		if entry.Owner != txstate.TransactionOwner || !isManagedNFTTarget(entry.Family, entry.Table) {
			errs = append(errs, fmt.Errorf("ambiguous nftables rollback target %s %s", entry.Family, entry.Table))
		}
	}
	for _, dns := range tx.Rollback.DNS {
		if dns.Owner != txstate.TransactionOwner || dns.Link != managedInterface || (dns.Backend != "" && dns.Backend != "systemd-resolved") {
			errs = append(errs, fmt.Errorf("ambiguous DNS rollback target link=%s backend=%s", dns.Link, dns.Backend))
		}
	}
	for _, rule := range tx.Rollback.PolicyRules {
		if rule.Owner != txstate.TransactionOwner {
			errs = append(errs, fmt.Errorf("ambiguous policy rule rollback priority %d", rule.Priority))
			continue
		}
		if _, ok := managedTableToken(rule.Table); !ok {
			errs = append(errs, fmt.Errorf("ambiguous policy rule rollback priority %d table %s", rule.Priority, rule.Table))
		}
	}
	for _, route := range tx.Rollback.Routes {
		if route.Owner != txstate.TransactionOwner {
			errs = append(errs, fmt.Errorf("ambiguous route rollback %s table %s", route.CIDR, route.Table))
			continue
		}
		if _, ok := managedTableToken(route.Table); !ok {
			errs = append(errs, fmt.Errorf("ambiguous route rollback %s table %s", route.CIDR, route.Table))
		}
		if strings.TrimSpace(route.Dev) != "" && route.Dev != managedInterface {
			errs = append(errs, fmt.Errorf("ambiguous route rollback %s device %s", route.CIDR, route.Dev))
		}
	}
	for _, tun := range tx.Rollback.TUN {
		if tun.Owner != txstate.TransactionOwner || tun.InterfaceName != managedInterface {
			errs = append(errs, fmt.Errorf("ambiguous TUN rollback target %s", tun.InterfaceName))
		}
	}
	for _, config := range tx.Rollback.GeneratedConfigs {
		if config.Owner != txstate.TransactionOwner {
			errs = append(errs, fmt.Errorf("ambiguous generated config rollback %s", config.Path))
			continue
		}
		if !isUnderDir(filepath.Join(runtimeDir, generatedDirName), filepath.Clean(config.Path)) {
			errs = append(errs, fmt.Errorf("ambiguous generated config rollback outside TunWarden runtime state: %s", config.Path))
		}
	}
	return errors.Join(errs...)
}

func daemonRollbackTransaction(ctx context.Context, exec OSCleanupExecutor, tx txstate.Transaction) error {
	var errs []error
	if err := exec.rollbackNFTables(ctx, tx.Rollback.NFTables); err != nil {
		errs = append(errs, err)
	}
	for i := len(tx.Rollback.DNS) - 1; i >= 0; i-- {
		if err := exec.rollbackDNS(ctx, tx.Rollback.DNS[i]); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(tx.Rollback.PolicyRules) - 1; i >= 0; i-- {
		if err := exec.rollbackPolicyRule(ctx, tx.Rollback.PolicyRules[i]); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(tx.Rollback.Routes) - 1; i >= 0; i-- {
		if err := exec.rollbackRoute(ctx, tx.Rollback.Routes[i]); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(tx.Rollback.TUN) - 1; i >= 0; i-- {
		if err := exec.rollbackTUN(ctx, tx.Rollback.TUN[i]); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(tx.Rollback.GeneratedConfigs) - 1; i >= 0; i-- {
		if err := exec.removeGeneratedConfig(tx.Rollback.GeneratedConfigs[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
