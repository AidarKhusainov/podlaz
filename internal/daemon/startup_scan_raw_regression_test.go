package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestFilterForStatusPreservesRawSnapshotForInactivePublication(t *testing.T) {
	runtimeDir := t.TempDir()
	configPath := filepath.Join(runtimeDir, "generated", "xray.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	tx := saveStartupScanCommittedTransaction(t, runtimeDir, "tx-active", "profile-test", configPath)
	status := startupScanActiveStatus(tx, configPath)
	state := &startupScanState{scan: recovery.PlanResult{Candidates: []recovery.Candidate{{Kind: "tun-interface", Target: "podlaz0"}}}}

	active := state.FilterForStatus(status, runtimeDir)
	if len(active.Candidates) != 0 {
		t.Fatalf("active owned resource must be hidden: %#v", active.Candidates)
	}

	status.Connection = "error (core exited)"
	afterExit := state.FilterForStatus(status, runtimeDir)
	if len(afterExit.Candidates) != 1 || afterExit.Candidates[0].Target != "podlaz0" {
		t.Fatalf("inactive publication must restore the raw stale candidate: %#v", afterExit.Candidates)
	}
	if raw := state.Snapshot(); len(raw.Candidates) != 1 || raw.Candidates[0].Target != "podlaz0" {
		t.Fatalf("filtering must not mutate the raw recovery snapshot: %#v", raw.Candidates)
	}
}

func TestActiveCommittedTransactionRefusesAmbiguousHeuristicMatches(t *testing.T) {
	runtimeDir := t.TempDir()
	configPath := filepath.Join(runtimeDir, "generated", "xray.json")
	oldTx := saveStartupScanCommittedTransaction(t, runtimeDir, "tx-old", "profile-test", configPath)
	newTx := saveStartupScanCommittedTransaction(t, runtimeDir, "tx-new", "profile-test", configPath)
	status := startupScanActiveStatus(oldTx, configPath)
	status.Transactions = append(status.Transactions, api.TransactionStatus{ID: newTx.ID, State: string(txstate.TransactionCommitted), Path: "unused-new"})

	_, ok, err := activeCommittedTransaction(status, runtimeDir)
	if ok || err == nil {
		t.Fatalf("ambiguous committed transactions must disable filtering: ok=%t err=%v", ok, err)
	}
}

func TestActiveCommittedTransactionUsesExactActiveTransactionID(t *testing.T) {
	runtimeDir := t.TempDir()
	configPath := filepath.Join(runtimeDir, "generated", "xray.json")
	oldTx := saveStartupScanCommittedTransaction(t, runtimeDir, "tx-old", "profile-test", configPath)
	newTx := saveStartupScanCommittedTransaction(t, runtimeDir, "tx-new", "profile-test", configPath)

	payload := []byte(`{
		"connection":"active",
		"mode":"tun",
		"profile_id":"profile-test",
		"runtime_config_path":"` + configPath + `",
		"active_transaction_id":"tx-new",
		"transactions":[
			{"id":"tx-old","state":"committed","path":"unused-old"},
			{"id":"tx-new","state":"committed","path":"unused-new"}
		]
	}`)
	var status api.StatusResponse
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatal(err)
	}

	selected, ok, err := activeCommittedTransaction(status, runtimeDir)
	if err != nil || !ok || selected.ID != newTx.ID {
		t.Fatalf("expected exact active transaction %s, got id=%s ok=%t err=%v (old=%s)", newTx.ID, selected.ID, ok, err, oldTx.ID)
	}
}

func saveStartupScanCommittedTransaction(t *testing.T, runtimeDir, id, profileID, configPath string) txstate.Transaction {
	t.Helper()
	tx := txstate.NewTransaction(id, profileID, planner.ModeTun, time.Now())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.Core = txstate.CorePlan{RuntimeConfigPath: configPath, Owner: txstate.TransactionOwner}
	tx.DesiredPlan.TUN = txstate.TUNDesiredState{InterfaceName: "podlaz0", Owner: xrayTunInboundOwner}
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}
	return tx
}

func startupScanActiveStatus(tx txstate.Transaction, configPath string) api.StatusResponse {
	return api.StatusResponse{
		Connection:        "active",
		Mode:              planner.ModeTun,
		ProfileID:         tx.ProfileID,
		RuntimeConfigPath: configPath,
		Transactions: []api.TransactionStatus{{
			ID: tx.ID, State: string(txstate.TransactionCommitted), Path: "unused-active",
		}},
	}
}
