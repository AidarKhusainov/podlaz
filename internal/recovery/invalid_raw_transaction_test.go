package recovery

import (
	"testing"
	"time"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func saveInvalidRawTransaction(t *testing.T, runtimeDir string, rollback txstate.RollbackMetadata) (string, txstate.Transaction) {
	t.Helper()
	tx := txstate.NewTransaction("tx-invalid", "profile-1", "tun", time.Now().UTC())
	tx.State = txstate.TransactionApplying
	tx.Rollback = rollback
	path, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx)
	if err != nil {
		t.Fatal(err)
	}
	return path, tx
}
