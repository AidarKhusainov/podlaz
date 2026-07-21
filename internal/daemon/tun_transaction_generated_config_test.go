package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestRollbackTunTransactionPreservesMetadataWhenGeneratedConfigRemovalFails(t *testing.T) {
	runtimeDir := t.TempDir()
	clock := fixedClock()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: clock}
	tx := txstate.NewTransaction("generated-config-cleanup-failure", "test-profile", planner.ModeTun, clock())

	configPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	if err := os.MkdirAll(configPath, 0o700); err != nil {
		t.Fatalf("create non-removable generated config path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configPath, "sentinel"), []byte("fixture"), 0o600); err != nil {
		t.Fatalf("create generated config sentinel: %v", err)
	}
	tx.Rollback.GeneratedConfigs = []txstate.GeneratedConfigRollback{{
		Path:  configPath,
		Owner: txstate.TransactionOwner,
	}}
	if _, err := store.Save(tx); err != nil {
		t.Fatalf("save transaction: %v", err)
	}

	err := rollbackTunTransaction(context.Background(), store, &tx, planner.TunPlan{}, &recordingTunExecutor{})
	if err == nil || !strings.Contains(err.Error(), "generated") {
		t.Fatalf("generated config cleanup failure must fail rollback, got %v", err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("failed cleanup fixture must remain for recovery: %v", err)
	}

	loaded, _, err := store.Load(tx.ID)
	if err != nil {
		t.Fatalf("transaction metadata must remain readable: %v", err)
	}
	if loaded.State == txstate.TransactionRolledBack {
		t.Fatalf("incomplete cleanup must not be marked rolled back: %#v", loaded)
	}

	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) != 0 || len(summaries) != 1 || !summaries[0].RequiresCleanup {
		t.Fatalf("incomplete generated config cleanup must remain recoverable: summaries=%#v warnings=%#v", summaries, warnings)
	}
}
