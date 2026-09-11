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

func finalizeSuccessfulNetworkSessionReplayEvidence(
	continuation networkSessionContinuationStore,
	record networkSessionResumeDiagnostic,
) error {
	seen := make(map[string]struct{}, 2)
	for _, attempt := range []*networkSessionReplayAttempt{record.Originating, record.Current} {
		if attempt == nil {
			continue
		}
		transactionID := strings.TrimSpace(attempt.TransactionID)
		if transactionID == "" || transactionID == noTunTransactionID {
			continue
		}
		if _, duplicate := seen[transactionID]; duplicate {
			continue
		}
		seen[transactionID] = struct{}{}
		if err := removeRetainedNetworkSessionReplayTransaction(continuation.runtimeDir, transactionID); err != nil {
			return fmt.Errorf("remove rolled-back replay transaction evidence %q after successful resume: %w", transactionID, err)
		}
	}
	return newNetworkSessionResumeDiagnosticStore(continuation.runtimeDir, continuation.readBootID).Remove()
}

func preflightRetainedNetworkSessionReplayEvidence(runtimeDir string, readBootID bootIDReader) error {
	diagnosticStore := newNetworkSessionResumeDiagnosticStore(runtimeDir, readBootID)
	record, diagnosticExists, err := diagnosticStore.Load()
	if err != nil {
		return err
	}
	if !diagnosticExists || !diagnosticHasTerminalReplayEvidence(record) {
		return nil
	}
	_, err = finalizableRolledBackTransactionEvidence(runtimeDir)
	return err
}

func finalizeRetainedNetworkSessionReplayEvidence(runtimeDir string, readBootID bootIDReader) error {
	diagnosticStore := newNetworkSessionResumeDiagnosticStore(runtimeDir, readBootID)
	record, diagnosticExists, err := diagnosticStore.Load()
	if err != nil {
		return err
	}
	if !diagnosticExists {
		return nil
	}
	if !diagnosticHasTerminalReplayEvidence(record) {
		// Generic startup/recovery diagnostics and non-terminal replay failures do
		// not own unrelated transaction state. A successful explicit lifecycle
		// finalization may discard that diagnostic, but must not inspect/delete
		// transactions merely because the diagnostic file exists.
		return diagnosticStore.Remove()
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
	return diagnosticStore.Remove()
}

func diagnosticHasTerminalReplayEvidence(record networkSessionResumeDiagnostic) bool {
	return record.Current != nil && record.Current.ReplayDisposition == networkSessionReplayDispositionTerminal
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
