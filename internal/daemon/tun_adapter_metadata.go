package daemon

import (
	"fmt"
	"time"

	txstate "github.com/AidarKhusainov/tunwarden/internal/state"
)

func saveTunAdapterRollbackMetadata(store txstate.TransactionStore, transactionID string, pid int, now time.Time) error {
	tx, _, err := store.Load(transactionID)
	if err != nil {
		return fmt.Errorf("load TUN transaction %s: %w", transactionID, err)
	}
	if pid > 0 {
		tx.Rollback.ChildProcesses = append(tx.Rollback.ChildProcesses, txstate.ChildProcessRollback{PID: pid, Label: "tun2socks", Owner: txstate.TransactionOwner})
	}
	tx.Health = txstate.HealthResult{Status: "tun-adapter-started", CheckedAt: now.UTC(), Message: "TUN adapter process stayed alive during startup verification"}
	_, err = store.Save(tx)
	return err
}
