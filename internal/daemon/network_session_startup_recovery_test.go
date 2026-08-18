package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type recordingStartupRecoveryExecutor struct {
	candidates []recovery.Candidate
}

func (e *recordingStartupRecoveryExecutor) Cleanup(_ context.Context, candidate recovery.Candidate) recovery.CleanupResult {
	e.candidates = append(e.candidates, candidate)
	return recovery.CleanupResult{Candidate: candidate, Status: "recovered"}
}

func TestExactNetworkSessionStartupRecoveryIncludesOnlyTransactionCandidates(t *testing.T) {
	runtimeDir := t.TempDir()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx := txstate.NewTransaction("startup-exact", "profile-example", "tun", time.Now().UTC())
	tx.State = txstate.TransactionApplying
	tx.Rollback.TUN = []txstate.TUNRollback{{InterfaceName: "podlaz0", Owner: txstate.TransactionOwner}}
	if _, err := store.Save(tx); err != nil {
		t.Fatalf("save transaction: %v", err)
	}
	generatedDir := filepath.Join(runtimeDir, generatedDirName)
	if err := os.MkdirAll(generatedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generatedDir, generatedXrayName), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	executor := &recordingStartupRecoveryExecutor{}
	response := recoverExactNetworkSessionTransactionsWithOptions(context.Background(), runtimeDir, recovery.Options{Executor: executor})
	if len(response.Warnings) != 0 {
		t.Fatalf("unexpected exact startup recovery warnings: %#v", response.Warnings)
	}
	if len(executor.candidates) != 1 {
		t.Fatalf("automatic startup recovery candidates = %#v", executor.candidates)
	}
	candidate := executor.candidates[0]
	if candidate.Kind != "transaction-state" || candidate.Transaction == nil || candidate.Transaction.ID != tx.ID {
		t.Fatalf("automatic startup recovery used non-exact candidate: %#v", candidate)
	}
}

func TestExactNetworkSessionStartupRecoveryFailsClosedOnTransactionInspectionWarning(t *testing.T) {
	runtimeDir := t.TempDir()
	transactionsDir := filepath.Join(runtimeDir, "transactions")
	if err := os.MkdirAll(transactionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transactionsDir, "broken.json"), []byte("{not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	executor := &recordingStartupRecoveryExecutor{}
	response := recoverExactNetworkSessionTransactionsWithOptions(context.Background(), runtimeDir, recovery.Options{Executor: executor})
	if len(response.Warnings) == 0 {
		t.Fatalf("corrupt exact transaction inspection must remain incomplete: %#v", response)
	}
	if len(executor.candidates) != 0 {
		t.Fatalf("inspection warning must not authorize guessed cleanup: %#v", executor.candidates)
	}
	if networkSessionRecoveryConverged(response) {
		t.Fatal("transaction inspection warning must keep startup recovery gate closed")
	}
}
