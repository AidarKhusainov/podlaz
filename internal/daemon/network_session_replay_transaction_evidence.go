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
	record, exists, err := diagnosticStore.Load()
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	transactionIDs := replayDiagnosticTransactionIDs(record)
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	for _, transactionID := range transactionIDs {
		tx, _, loadErr := store.Load(transactionID)
		if errors.Is(loadErr, os.ErrNotExist) {
			continue
		}
		if loadErr != nil {
			return fmt.Errorf("load retained replay transaction %q: %w", transactionID, loadErr)
		}
		if tx.State != txstate.TransactionRolledBack || tx.RequiresRecovery() {
			return fmt.Errorf("retained replay transaction %q still requires recovery", transactionID)
		}
	}
	for _, transactionID := range transactionIDs {
		if err := removeRetainedNetworkSessionReplayTransaction(runtimeDir, transactionID); err != nil {
			return fmt.Errorf("remove retained replay transaction %q: %w", transactionID, err)
		}
	}
	return diagnosticStore.Remove()
}

func replayDiagnosticTransactionIDs(record networkSessionResumeDiagnostic) []string {
	seen := make(map[string]struct{}, 2)
	ids := make([]string, 0, 2)
	for _, attempt := range []*networkSessionReplayAttempt{record.Originating, record.Current} {
		if attempt == nil {
			continue
		}
		transactionID := strings.TrimSpace(attempt.TransactionID)
		if transactionID == "" || transactionID == noTunTransactionID {
			continue
		}
		if _, ok := seen[transactionID]; ok {
			continue
		}
		seen[transactionID] = struct{}{}
		ids = append(ids, transactionID)
	}
	return ids
}
