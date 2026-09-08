package recovery

import (
	"context"
	"testing"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestExecuteConvergesStaleNftablesCandidateAfterExactTransactionRollback(t *testing.T) {
	runtimeDir := t.TempDir()
	tx := exactNftablesRecoveryTransaction()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	path, err := store.Save(tx)
	if err != nil {
		t.Fatalf("save exact nftables transaction: %v", err)
	}
	runner := newNftablesAuthorityRunner()
	standalone := Candidate{Kind: "nftables-table", Description: "nftables table", Target: "inet podlaz"}

	result := ExecuteWithOptions(context.Background(), Options{
		Scanner: fakeScanner{result: ScanResult{Candidates: []Candidate{
			transactionCandidate(path, tx),
			standalone,
		}}},
		Executor:   NetworkSessionCleanupExecutor{Runner: runner, RuntimeDir: runtimeDir},
		RuntimeDir: runtimeDir,
	})

	if result.HasFailures() || result.HasIncompleteCleanup() {
		t.Fatalf("exact transaction cleanup followed by stale nftables candidate must converge: %#v", result)
	}
	assertCleanupResult(t, result.Results, "transaction-state", "recovered", "")
	assertCleanupResult(t, result.Results, "nftables-table", "recovered", "already absent")
	if runner.directDeletes != 0 {
		t.Fatalf("recovery used name-only nft delete %d time(s)", runner.directDeletes)
	}
	if runner.guardedRemoves != 1 {
		t.Fatalf("generation-guarded removals=%d, want 1", runner.guardedRemoves)
	}
}
