package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
	statusreport "github.com/AidarKhusainov/podlaz/internal/status"
)

type fixedRecoveryScanner struct{ result recovery.ScanResult }

func (s fixedRecoveryScanner) Scan(context.Context) recovery.ScanResult { return s.result }

type countingCleanupExecutor struct{ calls int }

func (e *countingCleanupExecutor) Cleanup(_ context.Context, candidate recovery.Candidate) recovery.CleanupResult {
	e.calls++
	return recovery.CleanupResult{Candidate: candidate, Status: "recovered"}
}

func TestActiveCommittedRecoverExecutePerformsNoMutation(t *testing.T) {
	runtimeDir := t.TempDir()
	configPath := filepath.Join(runtimeDir, "generated", "xray.json")
	tx := txstate.NewTransaction("tx-live-recover", "profile-live", planner.ModeTun, time.Now().UTC())
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
	candidate := recovery.Candidate{Kind: "transaction-state", Target: path, Transaction: &recovery.TransactionCandidate{ID: tx.ID, State: string(txstate.TransactionCommitted), Path: path, RequiresCleanup: true}}
	executor := &countingCleanupExecutor{}
	response := daemonRecoverWithOptions(context.Background(), runtimeDir, status, recovery.Options{
		Scanner:  fixedRecoveryScanner{result: recovery.ScanResult{Candidates: []recovery.Candidate{candidate}}},
		Executor: executor,
	})
	if executor.calls != 0 {
		t.Fatalf("active committed recovery executed %d mutations", executor.calls)
	}
	assertActiveRecoverMutationFree(t, response)
}

func TestActiveCommittedStatusAndDoctorStayClean(t *testing.T) {
	runtimeDir := t.TempDir()
	configPath := filepath.Join(runtimeDir, "generated", "xray.json")
	tx := txstate.NewTransaction("tx-live-status", "profile-live", planner.ModeTun, time.Now().UTC())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.Core = txstate.CorePlan{RuntimeConfigPath: configPath, Owner: txstate.TransactionOwner}
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}
	manager := &XrayManager{RuntimeDir: runtimeDir}
	manager.state = xrayState{Connection: "active", Mode: planner.ModeTun, ProfileID: tx.ProfileID, RuntimeConfigPath: configPath, TransactionID: tx.ID}
	status := manager.statusForPublication(context.Background())
	if len(status.Transactions) != 1 || status.Transactions[0].RequiresCleanup {
		t.Fatalf("active committed status is unhealthy: %#v", status.Transactions)
	}
	report := statusreport.FromDaemon(status)
	if report.HasUnhealthyState() {
		t.Fatalf("active committed status returned unhealthy report: %#v", report)
	}
	doctor := manager.Doctor(context.Background())
	for _, check := range doctor.Checks {
		if strings.Contains(check.Message, tx.ID) && !strings.EqualFold(check.Severity, "ok") {
			t.Fatalf("active committed doctor published transaction warning: %#v", check)
		}
	}
}

func TestActiveCommittedRecoverWithOSScannerPerformsNoMutation(t *testing.T) {
	runtimeDir := t.TempDir()
	configPath := filepath.Join(runtimeDir, "generated", "xray.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	tx := txstate.NewTransaction("tx-live-os-recover", "profile-live", planner.ModeTun, time.Now().UTC())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.Core = txstate.CorePlan{RuntimeConfigPath: configPath, Owner: txstate.TransactionOwner}
	tx.DesiredPlan.TUN = txstate.TUNDesiredState{InterfaceName: "podlaz0", Owner: xrayTunInboundOwner}
	tx.Rollback.NFTables = []txstate.NFTablesRollback{{Family: "inet", Table: "podlaz", Owner: netexecutor.OwnerFirewall}}
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
	executor := &countingCleanupExecutor{}
	response := daemonRecoverWithOptions(context.Background(), runtimeDir, status, recovery.Options{
		Runner:   activeCommittedOSScanRunner{},
		Executor: executor,
	})
	if executor.calls != 0 {
		t.Fatalf("active committed OS recovery executed %d mutations", executor.calls)
	}
	assertActiveRecoverMutationFree(t, response)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active committed transaction was modified: %v", err)
	}
}

func assertActiveRecoverMutationFree(t *testing.T, response api.RecoveryResponse) {
	t.Helper()
	if len(response.Results) == 0 {
		t.Fatalf("active recovery should explicitly report mutation-free skipped candidates: %#v", response)
	}
	for _, result := range response.Results {
		if result.Status != "skipped" || !strings.Contains(result.Message, "active lifecycle session") {
			t.Fatalf("active recovery result is not mutation-free skipped: %#v", response)
		}
	}
	if len(response.Warnings) == 0 {
		t.Fatalf("active recovery should include a mutation-free warning: %#v", response)
	}
	for _, warning := range response.Warnings {
		if !strings.Contains(warning.Message, "active lifecycle session") {
			t.Fatalf("active recovery warning does not explain mutation-free behavior: %#v", response)
		}
	}
}
