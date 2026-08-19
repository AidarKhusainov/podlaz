package daemon

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestAutoRecoverTunOwnedStateRunsForExactRecoveryTransaction(t *testing.T) {
	m := NewXrayManager(t.TempDir())
	persistApplyingRecoveryFixture(t, m.runtimeDir(), "auto-recover-exact")

	previous := automaticPodlazRecover
	calls := 0
	automaticPodlazRecover = func(context.Context, string) error {
		calls++
		removeRecoveryFixtures(t, m.runtimeDir())
		return nil
	}
	t.Cleanup(func() { automaticPodlazRecover = previous })

	clean := snapshot.FakeResolvedDesktop()
	m.snapshotCollector = func(context.Context, snapshot.Options) snapshot.Snapshot { return clean }

	got, err := m.autoRecoverTunOwnedState(context.Background(), clean, api.HandoffBlock, snapshot.Options{})
	if err != nil {
		t.Fatalf("autoRecoverTunOwnedState() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one exact transaction recovery, got %d", calls)
	}
	if len(got.StaleResources) != 0 {
		t.Fatalf("expected refreshed clean snapshot, got %#v", got.StaleResources)
	}
}

func TestAutoRecoverTunOwnedStateAskRemainsReadOnly(t *testing.T) {
	m := NewXrayManager(t.TempDir())
	persistApplyingRecoveryFixture(t, m.runtimeDir(), "auto-recover-ask")
	previous := automaticPodlazRecover
	called := false
	automaticPodlazRecover = func(context.Context, string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { automaticPodlazRecover = previous })

	if _, err := m.autoRecoverTunOwnedState(context.Background(), snapshot.FakeResolvedDesktop(), api.HandoffAsk, snapshot.Options{}); err != nil {
		t.Fatalf("ask preflight should remain read-only: %v", err)
	}
	if called {
		t.Fatal("ask policy must not execute automatic recovery")
	}
}

func TestAutoRecoverTunOwnedStatePropagatesRecoveryFailure(t *testing.T) {
	m := NewXrayManager(t.TempDir())
	persistApplyingRecoveryFixture(t, m.runtimeDir(), "auto-recover-failure")
	previous := automaticPodlazRecover
	automaticPodlazRecover = func(context.Context, string) error { return errors.New("boom") }
	t.Cleanup(func() { automaticPodlazRecover = previous })

	if _, err := m.autoRecoverTunOwnedState(context.Background(), snapshot.FakeResolvedDesktop(), api.HandoffBlock, snapshot.Options{}); err == nil {
		t.Fatal("expected exact recovery failure to propagate")
	}
}

func persistApplyingRecoveryFixture(t *testing.T, runtimeDir, id string) {
	t.Helper()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }}
	tx := txstate.NewTransaction(id, "profile-1", planner.ModeTun, store.Now())
	if _, err := store.Save(tx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Transition(tx.ID, txstate.TransactionApplying); err != nil {
		t.Fatal(err)
	}
}

func removeRecoveryFixtures(t *testing.T, runtimeDir string) {
	t.Helper()
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) != 0 {
		t.Fatalf("scan recovery fixtures: %v", warnings)
	}
	for _, summary := range summaries {
		if err := os.Remove(summary.Path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}
