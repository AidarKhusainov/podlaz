package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func (s *startupScanState) FilterForStatus(status api.StatusResponse) recovery.PlanResult {
	if s == nil {
		return recovery.PlanResult{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scan = filterStartupScanForActiveRuntime(s.scan, status)
	return cloneRecoveryPlan(s.scan)
}

func filterStartupScanForActiveRuntime(scan recovery.PlanResult, status api.StatusResponse) recovery.PlanResult {
	out := cloneRecoveryPlan(scan)
	if status.Connection != "active" || status.Mode != planner.ModeTun {
		return out
	}
	tx, ok, err := activeCommittedTransaction(status)
	if err != nil {
		out.Warnings = append(out.Warnings, recovery.Warning{Target: "active TUN transaction", Message: err.Error()})
		return out
	}
	if !ok {
		out.Warnings = append(out.Warnings, recovery.Warning{Target: "active TUN transaction", Message: "could not identify a committed transaction owning the active runtime"})
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

func activeCommittedTransaction(status api.StatusResponse) (txstate.Transaction, bool, error) {
	store := txstate.TransactionStore{RuntimeDir: status.RuntimeDirectory}
	var loadErrors []string
	for _, summary := range status.Transactions {
		if summary.State != string(txstate.TransactionCommitted) || summary.RequiresCleanup {
			continue
		}
		tx, _, err := store.Load(summary.ID)
		if err != nil {
			loadErrors = append(loadErrors, err.Error())
			continue
		}
		if tx.Owner != txstate.TransactionOwner || tx.State != txstate.TransactionCommitted {
			continue
		}
		if status.ProfileID != "" && tx.ProfileID != status.ProfileID {
			continue
		}
		if status.RuntimeConfigPath != "" && filepath.Clean(tx.DesiredPlan.Core.RuntimeConfigPath) != filepath.Clean(status.RuntimeConfigPath) {
			continue
		}
		return tx, true, nil
	}
	if len(loadErrors) > 0 {
		return txstate.Transaction{}, false, fmt.Errorf("load committed active transaction: %s", strings.Join(loadErrors, "; "))
	}
	return txstate.Transaction{}, false, nil
}

func activeTransactionOwnsCandidate(tx txstate.Transaction, candidate recovery.Candidate) bool {
	switch candidate.Kind {
	case "tun-interface":
		for _, entry := range tx.Rollback.TUN {
			if entry.Owner == txstate.TransactionOwner && entry.InterfaceName == candidate.Target {
				return true
			}
		}
	case "dns-link":
		for _, entry := range tx.Rollback.DNS {
			if entry.Owner == txstate.TransactionOwner && entry.Link == candidate.Target {
				return true
			}
		}
	case "nftables-table":
		for _, entry := range tx.Rollback.NFTables {
			if entry.Owner == txstate.TransactionOwner && strings.TrimSpace(entry.Family+" "+entry.Table) == strings.TrimSpace(candidate.Target) {
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
