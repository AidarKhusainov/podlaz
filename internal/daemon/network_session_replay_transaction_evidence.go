package daemon

import (
	"errors"
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
