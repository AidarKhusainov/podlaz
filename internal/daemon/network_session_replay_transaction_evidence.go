package daemon

import (
	"errors"
	"fmt"
	"os"
	"strings"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func removeRetainedNetworkSessionReplayTransaction(runtimeDir, transactionID string) error {
	transactionID = strings.TrimSpace(transactionID)
	if transactionID == "" || transactionID == noTunTransactionID {
		return nil
	}
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx, _, err := store.Load(transactionID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if tx.State != txstate.TransactionRolledBack || tx.RequiresRecovery() {
		// Never erase live cleanup authority merely because a replay diagnostic
		// referenced the transaction.
		return nil
	}
	return removeTransactionFile(store, transactionID)
}

func finalizeRetainedNetworkSessionReplayEvidence(runtimeDir string, readBootID bootIDReader) error {
	diagnosticStore := newNetworkSessionResumeDiagnosticStore(runtimeDir, readBootID)
	_, diagnosticExists, err := diagnosticStore.Load()
	if err != nil {
		return err
	}

	transactionIDs, err := finalizableRolledBackTransactionEvidence(runtimeDir)
	if err != nil {
		return err
	}
	for _, transactionID := range transactionIDs {
		if err := removeRetainedNetworkSessionReplayTransaction(runtimeDir, transactionID); err != nil {
			return fmt.Errorf("remove rolled-back transaction evidence %q: %w", transactionID, err)
		}
	}
	if err := requireNoNetworkSessionTransactionState(runtimeDir); err != nil {
		return err
	}
	if !diagnosticExists {
		return nil
	}
	return diagnosticStore.Remove()
}

func finalizableRolledBackTransactionEvidence(runtimeDir string) ([]string, error) {
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) != 0 {
		return nil, fmt.Errorf("transaction finalization inspection is inconclusive: %s", strings.Join(warnings, "; "))
	}
	ids := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		if summary.RequiresRecovery {
			return nil, fmt.Errorf("transaction %q still requires recovery", summary.ID)
		}
		if summary.State != txstate.TransactionRolledBack {
			return nil, fmt.Errorf("transaction %q has unexpected non-recovery state %q", summary.ID, summary.State)
		}
		ids = append(ids, summary.ID)
	}
	return ids, nil
}

func requireNoNetworkSessionTransactionState(runtimeDir string) error {
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) != 0 {
		return fmt.Errorf("transaction finalization verification is inconclusive: %s", strings.Join(warnings, "; "))
	}
	if len(summaries) != 0 {
		return fmt.Errorf("transaction finalization left %d transaction record(s)", len(summaries))
	}
	return nil
}

func finalizeNetworkSessionReplayEvidenceAfterTeardown(
	continuation networkSessionContinuationStore,
	stateStore networkSessionStateStore,
) error {
	_, exists, err := stateStore.Load()
	if err != nil {
		return fmt.Errorf("inspect Network Session after terminal teardown: %w", err)
	}
	if exists {
		// Retained terminal authority means the caller still owns a durable
		// finalization step. Keep the diagnostic and exact transaction identity
		// until that step commits so a crash cannot erase the cleanup witness.
		return nil
	}
	return finalizeRetainedNetworkSessionReplayEvidence(continuation.runtimeDir, continuation.readBootID)
}
