package recovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestDaemonCleanupExecutorSkipsRuntimeRoot(t *testing.T) {
	runtimeDir := t.TempDir()
	marker := filepath.Join(runtimeDir, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := (DaemonCleanupExecutor{RuntimeDir: runtimeDir}).Cleanup(context.Background(), Candidate{
		Kind:        "runtime-directory",
		Description: "runtime directory",
		Target:      runtimeDir,
	})

	if result.Status != "skipped" {
		t.Fatalf("expected runtime root cleanup to be skipped, got %#v", result)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("runtime root child must remain after skipped cleanup: %v", err)
	}
}

func TestDaemonCleanupExecutorSkipsAmbiguousChildProcessMetadata(t *testing.T) {
	runtimeDir := t.TempDir()
	path, tx := saveTransaction(t, runtimeDir, txstate.RollbackMetadata{
		ChildProcesses: []txstate.ChildProcessRollback{{
			Label: "xray",
			PID:   424242,
			Owner: txstate.TransactionOwner,
		}},
	})

	results := (DaemonCleanupExecutor{RuntimeDir: runtimeDir}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "child-process", "skipped", "process identity cannot be verified")
	assertCleanupResult(t, results, "transaction-state", "skipped", "transaction state was preserved")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("transaction file must remain when cleanup is skipped: %v", err)
	}
}

func TestDaemonCleanupExecutorContinuesSafeCleanupWhenChildProcessIsSkipped(t *testing.T) {
	runtimeDir := t.TempDir()
	generatedPath := filepath.Join(runtimeDir, generatedDirName, "xray.json")
	if err := os.MkdirAll(filepath.Dir(generatedPath), 0o755); err != nil {
		t.Fatalf("create generated dir: %v", err)
	}
	if err := os.WriteFile(generatedPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write generated config: %v", err)
	}
	path, tx := saveTransaction(t, runtimeDir, txstate.RollbackMetadata{
		ChildProcesses: []txstate.ChildProcessRollback{{
			Label: "xray",
			PID:   424242,
			Owner: txstate.TransactionOwner,
		}},
		GeneratedConfigs: []txstate.GeneratedConfigRollback{{
			Path:  generatedPath,
			Owner: txstate.TransactionOwner,
		}},
	})

	results := (DaemonCleanupExecutor{RuntimeDir: runtimeDir}).CleanupMany(context.Background(), transactionCandidate(path, tx))

	assertCleanupResult(t, results, "child-process", "skipped", "process identity cannot be verified")
	assertCleanupResult(t, results, "generated-runtime-config", "recovered", "")
	assertCleanupResult(t, results, "transaction-state", "skipped", "transaction state was preserved")
	if _, err := os.Stat(generatedPath); !os.IsNotExist(err) {
		t.Fatalf("generated config must be removed even when child process is skipped, stat err=%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("transaction file must remain when cleanup is incomplete: %v", err)
	}
}

func saveTransaction(t *testing.T, runtimeDir string, rollback txstate.RollbackMetadata) (string, txstate.Transaction) {
	t.Helper()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx := txstate.NewTransaction("tx-child-process", "profile-1", "tun", time.Now().UTC())
	tx.State = txstate.TransactionApplying
	tx.Rollback = rollback
	path, err := store.Save(tx)
	if err != nil {
		t.Fatalf("save transaction: %v", err)
	}
	return path, tx
}

func transactionCandidate(path string, tx txstate.Transaction) Candidate {
	return Candidate{
		Kind:        "transaction-state",
		Description: "transaction rollback state",
		Target:      path,
		Transaction: &TransactionCandidate{
			ID:              tx.ID,
			State:           string(tx.State),
			Status:          "pending apply",
			RequiresCleanup: true,
			Path:            path,
		},
	}
}

func assertCleanupResult(t *testing.T, results []CleanupResult, kind string, status string, messageSubstring string) {
	t.Helper()
	for _, result := range results {
		if result.Candidate.Kind != kind || result.Status != status {
			continue
		}
		if messageSubstring == "" || strings.Contains(result.Message, messageSubstring) {
			return
		}
	}
	t.Fatalf("cleanup result kind=%q status=%q message containing %q not found in %#v", kind, status, messageSubstring, results)
}
