package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/doctor"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestFilterStartupScanExcludesOnlyResourcesOwnedByActiveCommittedTransaction(t *testing.T) {
	runtimeDir := t.TempDir()
	generatedDir := filepath.Join(runtimeDir, "generated")
	if err := os.MkdirAll(generatedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(generatedDir, "xray.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	tx := txstate.NewTransaction("tx-active", "profile-test", planner.ModeTun, time.Now())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.Core = txstate.CorePlan{RuntimeConfigPath: configPath, Owner: txstate.TransactionOwner}
	tx.DesiredPlan.TUN = txstate.TUNDesiredState{InterfaceName: "podlaz0", Owner: xrayTunInboundOwner}
	tx.Rollback.DNS = []txstate.DNSRollback{{Link: "podlaz0", Owner: netexecutor.OwnerDNS}}
	tx.Rollback.NFTables = []txstate.NFTablesRollback{{Family: "inet", Table: "podlaz", Owner: netexecutor.OwnerFirewall}}
	tx.Rollback.GeneratedConfigs = []txstate.GeneratedConfigRollback{{Path: configPath, Owner: txstate.TransactionOwner}}
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}

	scan := recovery.PlanResult{Candidates: []recovery.Candidate{
		{Kind: "tun-interface", Target: "podlaz0"},
		{Kind: "dns-link", Target: "podlaz0"},
		{Kind: "nftables-table", Target: "inet podlaz"},
		{Kind: "generated-runtime-configs", Target: generatedDir},
		{Kind: "nftables-table", Target: "inet foreign"},
	}}
	status := api.StatusResponse{
		Connection:          "active",
		Mode:                planner.ModeTun,
		ProfileID:           tx.ProfileID,
		RuntimeDirectory:    "present",
		RuntimeConfigPath:   configPath,
		ActiveTransactionID: tx.ID,
		Transactions: []api.TransactionStatus{{
			ID: tx.ID, State: string(txstate.TransactionCommitted), Path: filepath.Join(runtimeDir, txstate.TransactionDirName, tx.ID+txstate.TransactionFileSuffix),
		}},
	}

	state := &startupScanState{scan: scan}
	filtered := state.FilterForStatus(status, runtimeDir)
	if len(filtered.Candidates) != 1 || filtered.Candidates[0].Target != "inet foreign" {
		t.Fatalf("active resources were not filtered precisely: %#v", filtered.Candidates)
	}
	if len(filtered.Warnings) != 0 {
		t.Fatalf("unexpected filter warnings: %#v", filtered.Warnings)
	}

	activeOnly := recovery.PlanResult{Candidates: append([]recovery.Candidate(nil), scan.Candidates[:4]...)}
	activeState := &startupScanState{scan: activeOnly}
	activeFiltered := activeState.FilterForStatus(status, runtimeDir)
	publishedStatus := withStartupScanStatus(status, activeFiltered)
	if publishedStatus.StartupScan == nil || publishedStatus.StartupScan.Status != api.StartupScanStatusClean || len(publishedStatus.StartupScan.Candidates) != 0 {
		t.Fatalf("active resources were published as stale in status: %#v", publishedStatus.StartupScan)
	}
	publishedDoctor := withStartupScanDoctor(api.DoctorResponse{}, activeFiltered)
	if len(publishedDoctor.Checks) == 0 || publishedDoctor.Checks[len(publishedDoctor.Checks)-1].Severity != string(doctor.SeverityOK) {
		t.Fatalf("active resources were published as stale in doctor: %#v", publishedDoctor.Checks)
	}
	if raw := activeState.Snapshot(); len(raw.Candidates) != 4 {
		t.Fatalf("publication filtering mutated raw recovery state: %#v", raw.Candidates)
	}
}

func TestFilterStartupScanKeepsGeneratedDirectoryContainingUnownedFiles(t *testing.T) {
	runtimeDir := t.TempDir()
	generatedDir := filepath.Join(runtimeDir, "generated")
	if err := os.MkdirAll(generatedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(generatedDir, "active.json")
	for _, name := range []string{"active.json", "stale.json"} {
		if err := os.WriteFile(filepath.Join(generatedDir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tx := txstate.NewTransaction("tx-active", "profile-test", planner.ModeTun, time.Now())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.Core = txstate.CorePlan{RuntimeConfigPath: configPath, Owner: txstate.TransactionOwner}
	tx.Rollback.GeneratedConfigs = []txstate.GeneratedConfigRollback{{Path: configPath, Owner: txstate.TransactionOwner}}
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}
	status := api.StatusResponse{
		Connection: "active", Mode: planner.ModeTun, ProfileID: tx.ProfileID,
		RuntimeDirectory: "present", RuntimeConfigPath: configPath, ActiveTransactionID: tx.ID,
		Transactions: []api.TransactionStatus{{ID: tx.ID, State: string(txstate.TransactionCommitted), Path: "unused"}},
	}
	candidate := recovery.Candidate{Kind: "generated-runtime-configs", Target: generatedDir}
	filtered := filterStartupScanForActiveRuntime(recovery.PlanResult{Candidates: []recovery.Candidate{candidate}}, status, runtimeDir)
	if len(filtered.Candidates) != 1 {
		t.Fatalf("directory with unowned stale file must remain recoverable: %#v", filtered.Candidates)
	}
}
