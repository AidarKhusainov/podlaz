package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func (s *startupScanState) FilterForStatus(status api.StatusResponse, runtimeDir string) recovery.PlanResult {
	if s == nil {
		return recovery.PlanResult{}
	}
	return filterStartupScanForActiveRuntime(s.Snapshot(), status, runtimeDir)
}

func filterStartupScanForActiveRuntime(scan recovery.PlanResult, status api.StatusResponse, runtimeDir string) recovery.PlanResult {
	out := cloneRecoveryPlan(scan)
	if status.Connection != "active" || !supportsActiveRuntimeOwnershipFiltering(status.Mode) {
		return out
	}
	if status.Mode == planner.ModeProxyOnly {
		return filterStartupScanForActiveProxyOnly(out, status)
	}
	tx, ok, err := activeCommittedTransaction(status, runtimeDir)
	if err != nil {
		out.Warnings = append(out.Warnings, recovery.Warning{Target: "active transaction", Message: err.Error()})
		return out
	}
	if !ok {
		out.Warnings = append(out.Warnings, recovery.Warning{Target: "active transaction", Message: "could not identify a committed transaction owning the active runtime"})
		return out
	}
	filtered := out.Candidates[:0]
	for _, candidate := range out.Candidates {
		if activeTransactionOwnsCandidate(tx, candidate) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	out.Candidates = filtered
	return out
}

func filterStartupScanForActiveProxyOnly(scan recovery.PlanResult, status api.StatusResponse) recovery.PlanResult {
	out := cloneRecoveryPlan(scan)
	filtered := out.Candidates[:0]
	for _, candidate := range out.Candidates {
		if activeProxyOnlyOwnsGeneratedDirectory(status, candidate) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	out.Candidates = filtered
	return out
}

func activeProxyOnlyOwnsGeneratedDirectory(status api.StatusResponse, candidate recovery.Candidate) bool {
	if candidate.Kind != "generated-runtime-configs" {
		return false
	}
	runtimeConfigPath := strings.TrimSpace(status.RuntimeConfigPath)
	if runtimeConfigPath == "" {
		return false
	}
	target := filepath.Clean(candidate.Target)
	configPath := filepath.Clean(runtimeConfigPath)
	if filepath.Dir(configPath) != target {
		return false
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 1 || entries[0].IsDir() {
		return false
	}
	return filepath.Join(target, entries[0].Name()) == configPath
}

func supportsActiveRuntimeOwnershipFiltering(mode string) bool {
	switch mode {
	case planner.ModeProxyOnly, planner.ModeTun:
		return true
	default:
		return false
	}
}

func activeCommittedTransaction(status api.StatusResponse, runtimeDir string) (txstate.Transaction, bool, error) {
	activeID := strings.TrimSpace(status.ActiveTransactionID)
	if activeID == "" {
		return txstate.Transaction{}, false, fmt.Errorf("active status has no active transaction id; refusing ownership filtering")
	}

	matches := 0
	var activeSummary api.TransactionStatus
	for _, summary := range status.Transactions {
		if summary.ID != activeID {
			continue
		}
		matches++
		activeSummary = summary
	}
	if matches != 1 {
		return txstate.Transaction{}, false, fmt.Errorf("active transaction %s has %d status matches; refusing ownership filtering", activeID, matches)
	}
	if activeSummary.State != string(txstate.TransactionCommitted) {
		return txstate.Transaction{}, false, fmt.Errorf("active transaction %s is not a clean committed transaction", activeID)
	}

	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx, loadedPath, err := store.Load(activeID)
	if err != nil {
		return txstate.Transaction{}, false, fmt.Errorf("load active committed transaction %s: %w", activeID, err)
	}
	if strings.TrimSpace(activeSummary.Path) == "" || filepath.Clean(activeSummary.Path) != filepath.Clean(loadedPath) {
		return txstate.Transaction{}, false, fmt.Errorf("active transaction %s state path does not match the authoritative store path", activeID)
	}
	if tx.Owner != txstate.TransactionOwner || tx.State != txstate.TransactionCommitted {
		return txstate.Transaction{}, false, fmt.Errorf("active transaction %s has invalid owner or state", activeID)
	}
	if tx.Mode != status.Mode {
		return txstate.Transaction{}, false, fmt.Errorf("active transaction %s mode does not match status", activeID)
	}
	if status.ProfileID != "" && tx.ProfileID != status.ProfileID {
		return txstate.Transaction{}, false, fmt.Errorf("active transaction %s profile does not match status", activeID)
	}
	if status.RuntimeConfigPath != "" && filepath.Clean(tx.DesiredPlan.Core.RuntimeConfigPath) != filepath.Clean(status.RuntimeConfigPath) {
		return txstate.Transaction{}, false, fmt.Errorf("active transaction %s runtime config does not match status", activeID)
	}
	return tx, true, nil
}

func activeTransactionOwnsCandidate(tx txstate.Transaction, candidate recovery.Candidate) bool {
	switch candidate.Kind {
	case "transaction-state":
		if candidate.Transaction == nil || candidate.Transaction.ID != tx.ID || candidate.Transaction.State != string(txstate.TransactionCommitted) {
			return false
		}
		return filepath.Clean(candidate.Target) == filepath.Clean(candidate.Transaction.Path) &&
			filepath.Base(candidate.Target) == tx.ID+txstate.TransactionFileSuffix
	case "tun-interface":
		return tx.DesiredPlan.TUN.Owner == xrayTunInboundOwner && tx.DesiredPlan.TUN.InterfaceName == candidate.Target
	case "dns-link":
		for _, entry := range tx.Rollback.DNS {
			if entry.Owner == netexecutor.OwnerDNS && entry.Link == candidate.Target {
				return true
			}
		}
	case "nftables-table":
		for _, entry := range tx.Rollback.NFTables {
			if entry.Owner == netexecutor.OwnerFirewall && strings.TrimSpace(entry.Family+" "+entry.Table) == strings.TrimSpace(candidate.Target) {
				return true
			}
		}
	case "generated-runtime-configs":
		return activeTransactionOwnsGeneratedDirectory(tx, candidate.Target)
	}
	return false
}

func activeTransactionOwnsGeneratedDirectory(tx txstate.Transaction, target string) bool {
	target = filepath.Clean(target)
	owned := make(map[string]struct{})
	for _, entry := range tx.Rollback.GeneratedConfigs {
		if entry.Owner != txstate.TransactionOwner || filepath.Dir(filepath.Clean(entry.Path)) != target {
			continue
		}
		owned[filepath.Clean(entry.Path)] = struct{}{}
	}
	if len(owned) == 0 {
		return false
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return false
		}
		if _, ok := owned[filepath.Join(target, entry.Name())]; !ok {
			return false
		}
	}
	return true
}
