package daemon

import (
	"errors"
	"os"
	"testing"
	"time"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestFinalizeKeepsReplayEvidenceWhenSessionAuthorityRemovalFails(t *testing.T) {
	stateStore, state := admittedTerminalSessionForFinalizeTest(t)
	continuation := newNetworkSessionContinuationStore(stateStore.runtimeDir, fixedBootID("boot-a"))
	txStore := txstate.TransactionStore{RuntimeDir: stateStore.runtimeDir}

	tx := txstate.NewTransaction("tun-finalize-ordering", "profile-test", "tun", time.Now().UTC())
	tx.State = txstate.TransactionRolledBack
	txPath, err := txStore.Save(tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveFinalizeReplayDiagnostic(stateStore, state, tx.ID); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected Network Session authority removal failure")
	err = finalizeNetworkSessionWithAuthorityRemoval(continuation, func() error { return injected })
	if !errors.Is(err, injected) {
		t.Fatalf("finalize error=%v, want injected authority removal failure", err)
	}
	if _, err := os.Stat(txPath); err != nil {
		t.Fatalf("rolled-back transaction evidence was removed before Network Session authority: %v", err)
	}
	if _, exists, err := newNetworkSessionResumeDiagnosticStore(stateStore.runtimeDir, stateStore.readBootID).Load(); err != nil || !exists {
		t.Fatalf("replay diagnostic was removed before Network Session authority: exists=%v err=%v", exists, err)
	}
	if _, exists, err := stateStore.Load(); err != nil || !exists {
		t.Fatalf("Network Session authority disappeared after injected removal failure: exists=%v err=%v", exists, err)
	}
}
