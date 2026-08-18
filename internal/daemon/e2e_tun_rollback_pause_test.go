package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type rollbackPauseExecutor struct {
	rollbackCalled chan struct{}
}

func (e rollbackPauseExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, nil
}

func (e rollbackPauseExecutor) Verify(context.Context, planner.TunPlan) error {
	return nil
}

func (e rollbackPauseExecutor) Rollback(context.Context, planner.TunPlan) error {
	close(e.rollbackCalled)
	return nil
}

func TestE2ETunRollbackPauseOccursAfterRollingBackStateIsPersisted(t *testing.T) {
	runtimeDir := t.TempDir()
	hookDir := t.TempDir()
	t.Setenv(e2eTunRollbackPauseEnv, "true")
	t.Setenv(e2eTunRollbackPauseDirEnv, hookDir)
	t.Setenv(e2eTunRollbackPauseTimeoutEnv, "5")
	if err := os.WriteFile(filepath.Join(hookDir, e2eTunRollbackPauseArmFile), []byte("armed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: func() time.Time { return now }}
	tx := txstate.NewTransaction("rollback-pause", "profile-example", planner.ModeTun, now)
	tx.State = txstate.TransactionCommitted
	path, err := store.Save(tx)
	if err != nil {
		t.Fatalf("save committed transaction: %v", err)
	}
	executor := rollbackPauseExecutor{rollbackCalled: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- rollbackTunTransactionWithChildStopper(
			context.Background(), store, &tx, planner.TunPlan{Mode: planner.ModeTun}, executor,
			func(txstate.Transaction) error { return nil },
		)
	}()

	ready := filepath.Join(hookDir, e2eTunRollbackPauseReadyFile)
	waitForFile(t, ready)
	persisted, err := txstate.LoadTransactionFile(path)
	if err != nil {
		t.Fatalf("load transaction at rollback pause: %v", err)
	}
	if persisted.State != txstate.TransactionRollingBack {
		t.Fatalf("persisted transaction state = %q, want %q", persisted.State, txstate.TransactionRollingBack)
	}
	select {
	case <-executor.rollbackCalled:
		t.Fatal("host rollback began before deterministic pause")
	default:
	}
	if _, err := os.Stat(filepath.Join(hookDir, e2eTunRollbackPauseArmFile)); !os.IsNotExist(err) {
		t.Fatalf("rollback pause arm must be consumed exactly once: %v", err)
	}

	if err := os.WriteFile(filepath.Join(hookDir, e2eTunRollbackPauseContinue), []byte("continue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("rollback after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rollback did not resume after continue marker")
	}
	select {
	case <-executor.rollbackCalled:
	default:
		t.Fatal("host rollback did not run after release")
	}
}

func TestE2ETunRollbackPauseIsNoOpWhenNotArmed(t *testing.T) {
	t.Setenv(e2eTunRollbackPauseEnv, "true")
	t.Setenv(e2eTunRollbackPauseDirEnv, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := maybePauseForE2ETunRollback(ctx); err != nil {
		t.Fatalf("unarmed rollback pause must be a no-op: %v", err)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
