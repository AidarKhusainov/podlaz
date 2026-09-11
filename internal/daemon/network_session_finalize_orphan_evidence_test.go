package daemon

import (
	"os"
	"testing"
	"time"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestFinalizeRemovesUnreferencedRolledBackTransactionEvidence(t *testing.T) {
	stateStore, state := admittedTerminalSessionForFinalizeTest(t)
	continuation := newNetworkSessionContinuationStore(stateStore.runtimeDir, fixedBootID("boot-a"))
	txStore := txstate.TransactionStore{RuntimeDir: stateStore.runtimeDir}

	referenced := txstate.NewTransaction("tun-finalize-current", "profile-test", "tun", time.Now().UTC())
	referenced.State = txstate.TransactionRolledBack
	if _, err := txStore.Save(referenced); err != nil {
		t.Fatal(err)
	}
	orphan := txstate.NewTransaction("tun-finalize-orphan", "profile-test", "tun", time.Now().UTC())
	orphan.State = txstate.TransactionRolledBack
	orphanPath, err := txStore.Save(orphan)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveFinalizeReplayDiagnostic(stateStore, state, referenced.ID); err != nil {
		t.Fatal(err)
	}

	if err := continuation.finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("unreferenced rolled-back transaction evidence remains: %v", err)
	}
}

func TestFinalizeKeepsSessionWhenUnreferencedTransactionNeedsRecovery(t *testing.T) {
	stateStore, state := admittedTerminalSessionForFinalizeTest(t)
	continuation := newNetworkSessionContinuationStore(stateStore.runtimeDir, fixedBootID("boot-a"))
	txStore := txstate.TransactionStore{RuntimeDir: stateStore.runtimeDir}

	referenced := txstate.NewTransaction("tun-finalize-current", "profile-test", "tun", time.Now().UTC())
	referenced.State = txstate.TransactionRolledBack
	referencedPath, err := txStore.Save(referenced)
	if err != nil {
		t.Fatal(err)
	}
	unresolved := txstate.NewTransaction("tun-finalize-unresolved", "profile-test", "tun", time.Now().UTC())
	unresolved.State = txstate.TransactionFailed
	unresolvedPath, err := txStore.Save(unresolved)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveFinalizeReplayDiagnostic(stateStore, state, referenced.ID); err != nil {
		t.Fatal(err)
	}

	if err := continuation.finalize(); err == nil {
		t.Fatal("expected unreferenced recovery-required transaction to block finalization")
	}
	if _, err := os.Stat(referencedPath); err != nil {
		t.Fatalf("referenced evidence was removed before all transaction authority converged: %v", err)
	}
	if _, err := os.Stat(unresolvedPath); err != nil {
		t.Fatalf("unresolved transaction authority was removed: %v", err)
	}
	if _, exists, err := stateStore.Load(); err != nil || !exists {
		t.Fatalf("session authority was removed: exists=%v err=%v", exists, err)
	}
}
