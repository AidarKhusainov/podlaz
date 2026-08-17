package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestIssue254ProxyOnlyCommittedRuntimeIsNotPublishedAsStale(t *testing.T) {
	runtimeDir := t.TempDir()
	generatedDir := filepath.Join(runtimeDir, "generated")
	if err := os.MkdirAll(generatedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(generatedDir, "xray.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	tx := txstate.NewTransaction("tx-proxy-active", "profile-test", planner.ModeProxyOnly, time.Now().UTC())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.Core = txstate.CorePlan{RuntimeConfigPath: configPath, Owner: txstate.TransactionOwner}
	tx.Rollback.GeneratedConfigs = []txstate.GeneratedConfigRollback{{Path: configPath, Owner: txstate.TransactionOwner}}
	path, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx)
	if err != nil {
		t.Fatal(err)
	}

	scan := recovery.PlanResult{Candidates: []recovery.Candidate{
		{
			Kind:   "transaction-state",
			Target: path,
			Transaction: &recovery.TransactionCandidate{
				ID: tx.ID, State: string(txstate.TransactionCommitted), Path: path, RequiresCleanup: true,
			},
		},
		{Kind: "generated-runtime-configs", Target: generatedDir},
	}}
	status := api.StatusResponse{
		Connection:          "active",
		Mode:                planner.ModeProxyOnly,
		ProfileID:           tx.ProfileID,
		RuntimeDirectory:    "present",
		RuntimeConfigPath:   configPath,
		ActiveTransactionID: tx.ID,
		Transactions: []api.TransactionStatus{{
			ID: tx.ID, State: string(txstate.TransactionCommitted), Path: path,
		}},
	}

	filtered := filterStartupScanForActiveRuntime(scan, status, runtimeDir)
	if len(filtered.Candidates) != 0 {
		t.Fatalf("active proxy-only resources were published as stale: %#v", filtered.Candidates)
	}
	if len(filtered.Warnings) != 0 {
		t.Fatalf("active proxy-only ownership filter produced warnings: %#v", filtered.Warnings)
	}
	published := withStartupScanStatus(status, filtered)
	if published.StartupScan == nil || published.StartupScan.Status != api.StartupScanStatusClean {
		t.Fatalf("active proxy-only startup publication is not clean: %#v", published.StartupScan)
	}
}

func TestIssue254ActiveRuntimeOwnershipFilterFailsClosedOnModeMismatch(t *testing.T) {
	runtimeDir := t.TempDir()
	tx := txstate.NewTransaction("tx-mode-mismatch", "profile-test", planner.ModeTun, time.Now().UTC())
	tx.State = txstate.TransactionCommitted
	path, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx)
	if err != nil {
		t.Fatal(err)
	}

	candidate := recovery.Candidate{
		Kind:   "transaction-state",
		Target: path,
		Transaction: &recovery.TransactionCandidate{
			ID: tx.ID, State: string(txstate.TransactionCommitted), Path: path, RequiresCleanup: true,
		},
	}
	status := api.StatusResponse{
		Connection:          "active",
		Mode:                planner.ModeProxyOnly,
		ProfileID:           tx.ProfileID,
		ActiveTransactionID: tx.ID,
		Transactions: []api.TransactionStatus{{
			ID: tx.ID, State: string(txstate.TransactionCommitted), Path: path,
		}},
	}

	filtered := filterStartupScanForActiveRuntime(recovery.PlanResult{Candidates: []recovery.Candidate{candidate}}, status, runtimeDir)
	if len(filtered.Candidates) != 1 {
		t.Fatalf("mode-mismatched transaction was incorrectly filtered: %#v", filtered.Candidates)
	}
	if len(filtered.Warnings) != 1 || !strings.Contains(filtered.Warnings[0].Message, "mode does not match status") {
		t.Fatalf("mode mismatch warning = %#v", filtered.Warnings)
	}
}
