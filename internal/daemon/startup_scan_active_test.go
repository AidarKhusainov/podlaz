package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
			ID: tx.ID, State: string(txstate.TransactionCommitted), Path: filepath.Join(runtimeDir, txstate.TransactionDirName, tx.ID+txstate.TransactionFileSuffix), RequiresCleanup: false,
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
	path, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx)
	if err != nil {
		t.Fatal(err)
	}
	status := api.StatusResponse{
		Connection: "active", Mode: planner.ModeTun, ProfileID: tx.ProfileID,
		RuntimeDirectory: "present", RuntimeConfigPath: configPath, ActiveTransactionID: tx.ID,
		Transactions: []api.TransactionStatus{{ID: tx.ID, State: string(txstate.TransactionCommitted), Path: path}},
	}
	candidate := recovery.Candidate{Kind: "generated-runtime-configs", Target: generatedDir}
	filtered := filterStartupScanForActiveRuntime(recovery.PlanResult{Candidates: []recovery.Candidate{candidate}}, status, runtimeDir)
	if len(filtered.Candidates) != 1 {
		t.Fatalf("directory with unowned stale file must remain recoverable: %#v", filtered.Candidates)
	}
}

func TestFilterStartupScanRemovesExactActiveTransactionCandidate(t *testing.T) {
	runtimeDir := t.TempDir()
	configPath := filepath.Join(runtimeDir, "generated", "xray.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	tx := txstate.NewTransaction("tx-active-candidate", "profile-test", planner.ModeTun, time.Now())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.Core = txstate.CorePlan{RuntimeConfigPath: configPath, Owner: txstate.TransactionOwner}
	path, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx)
	if err != nil {
		t.Fatal(err)
	}
	status := api.StatusResponse{
		Connection: "active", Mode: planner.ModeTun, ProfileID: tx.ProfileID,
		RuntimeConfigPath: configPath, ActiveTransactionID: tx.ID,
		Transactions: []api.TransactionStatus{{ID: tx.ID, State: string(txstate.TransactionCommitted), Path: path}},
	}
	scan := recovery.PlanResult{Candidates: []recovery.Candidate{{
		Kind: "transaction-state", Target: path,
		Transaction: &recovery.TransactionCandidate{ID: tx.ID, State: string(txstate.TransactionCommitted), Path: path, RequiresCleanup: true},
	}}}
	filtered := filterStartupScanForActiveRuntime(scan, status, runtimeDir)
	if len(filtered.Candidates) != 0 || len(filtered.Warnings) != 0 {
		t.Fatalf("active committed transaction candidate was not filtered: %#v", filtered)
	}
}

type activeCommittedOSScanRunner struct{}

func (activeCommittedOSScanRunner) LookPath(file string) (string, error) {
	switch file {
	case "ip", "nft", "resolvectl":
		return "/usr/bin/" + file, nil
	default:
		return "", errors.New("command not found")
	}
}

func (activeCommittedOSScanRunner) Run(_ context.Context, name string, args ...string) (recovery.CommandResult, error) {
	key := filepath.Base(name) + " " + strings.Join(args, " ")
	switch key {
	case "ip link show dev podlaz0":
		return recovery.CommandResult{Stdout: "7: podlaz0: <POINTOPOINT,UP> mtu 1500 state UNKNOWN mode DEFAULT group default qlen 500\\n    link/none"}, nil
	case "nft list table inet podlaz":
		return recovery.CommandResult{Stdout: "table inet podlaz {}"}, nil
	case "resolvectl status podlaz0 --no-pager":
		return recovery.CommandResult{Stdout: "Link 7 (podlaz0)\\n    Current Scopes: DNS"}, nil
	default:
		return recovery.CommandResult{ExitCode: -1}, errors.New("unexpected command: " + key)
	}
}

func TestOSScannerAndActiveFilterDoNotPublishLiveCommittedTransaction(t *testing.T) {
	runtimeDir := t.TempDir()
	generatedDir := filepath.Join(runtimeDir, "generated")
	if err := os.MkdirAll(generatedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(generatedDir, "xray.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	tx := txstate.NewTransaction("tx-active-os-scan", "profile-test", planner.ModeTun, time.Now().UTC())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.Core = txstate.CorePlan{RuntimeConfigPath: configPath, Owner: txstate.TransactionOwner}
	tx.DesiredPlan.TUN = txstate.TUNDesiredState{InterfaceName: "podlaz0", Owner: xrayTunInboundOwner}
	tx.Rollback.NFTables = []txstate.NFTablesRollback{{Family: "inet", Table: "podlaz", Owner: netexecutor.OwnerFirewall}}
	tx.Rollback.GeneratedConfigs = []txstate.GeneratedConfigRollback{{Path: configPath, Owner: txstate.TransactionOwner}}
	path, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx)
	if err != nil {
		t.Fatal(err)
	}

	scan := recovery.PlanWithOptions(context.Background(), recovery.Options{
		RuntimeDir: runtimeDir,
		Runner:     activeCommittedOSScanRunner{},
	})
	foundTransaction := false
	for _, candidate := range scan.Candidates {
		if candidate.Kind == "transaction-state" && candidate.Transaction != nil && candidate.Transaction.ID == tx.ID {
			foundTransaction = true
		}
	}
	if !foundTransaction {
		t.Fatalf("OSScanner did not publish committed restart candidate before active filtering: %#v", scan.Candidates)
	}
	status := api.StatusResponse{
		Connection: "active", Mode: planner.ModeTun, ProfileID: tx.ProfileID,
		RuntimeDirectory: "present", RuntimeConfigPath: configPath, ActiveTransactionID: tx.ID,
		Transactions: []api.TransactionStatus{{ID: tx.ID, State: string(txstate.TransactionCommitted), Path: path}},
	}
	filtered := filterStartupScanForActiveRuntime(scan, status, runtimeDir)
	if len(filtered.Candidates) != 0 || len(filtered.Warnings) != 0 {
		t.Fatalf("live committed transaction remained stale after OSScanner composition: %#v", filtered)
	}
	published := withStartupScanStatus(status, filtered)
	if published.StartupScan == nil || published.StartupScan.Status != api.StartupScanStatusClean {
		t.Fatalf("live committed transaction did not publish clean startup status: %#v", published.StartupScan)
	}
}
