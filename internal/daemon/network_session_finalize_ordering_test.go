package daemon

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
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

func TestFinalizeKeepsReadOnlyReplayEvidenceWhenAuthorityRemovalReportsPostUnlinkFailure(t *testing.T) {
	stateStore, state := admittedTerminalSessionForFinalizeTest(t)
	continuation := newNetworkSessionContinuationStore(stateStore.runtimeDir, fixedBootID("boot-a"))
	txStore := txstate.TransactionStore{RuntimeDir: stateStore.runtimeDir}

	tx := txstate.NewTransaction("tun-finalize-post-unlink", "profile-test", "tun", time.Now().UTC())
	tx.State = txstate.TransactionRolledBack
	txPath, err := txStore.Save(tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveFinalizeReplayDiagnostic(stateStore, state, tx.ID); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected post-unlink durability failure")
	err = finalizeNetworkSessionWithAuthorityRemoval(continuation, func() error {
		if err := stateStore.Remove(); err != nil {
			return err
		}
		// Model the observable result of an unlink that succeeded but whose
		// subsequent durability confirmation failed. Evidence must remain because
		// the caller cannot know whether the authority removal committed durably.
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("finalize error=%v, want injected post-unlink failure", err)
	}
	if _, exists, err := stateStore.Load(); err != nil || exists {
		t.Fatalf("Network Session authority should already be absent: exists=%v err=%v", exists, err)
	}
	if _, err := os.Stat(txPath); err != nil {
		t.Fatalf("rolled-back transaction evidence was removed after uncertain authority durability: %v", err)
	}
	if _, exists, err := newNetworkSessionResumeDiagnosticStore(stateStore.runtimeDir, stateStore.readBootID).Load(); err != nil || !exists {
		t.Fatalf("replay diagnostic was removed after uncertain authority durability: exists=%v err=%v", exists, err)
	}
}

func TestNoSessionRetryFinalizesRetainedTerminalReplayEvidence(t *testing.T) {
	stateStore, state := admittedTerminalSessionForFinalizeTest(t)
	continuation := newNetworkSessionContinuationStore(stateStore.runtimeDir, fixedBootID("boot-a"))
	txStore := txstate.TransactionStore{RuntimeDir: stateStore.runtimeDir}

	tx := txstate.NewTransaction("tun-finalize-retry", "profile-test", "tun", time.Now().UTC())
	tx.State = txstate.TransactionRolledBack
	txPath, err := txStore.Save(tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveFinalizeReplayDiagnostic(stateStore, state, tx.ID); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Remove(); err != nil {
		t.Fatalf("remove Network Session authority before retry: %v", err)
	}

	continuation.migrateLegacy = func(string, networkSessionContinuationStore) (bool, error) { return false, nil }
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
		return api.RecoveryResponse{Mode: "execute"}
	}

	for attempt := 1; attempt <= 2; attempt++ {
		resumed, err := resumeNetworkSession(context.Background(), continuation, nil, nil, nil)
		if err != nil || resumed {
			t.Fatalf("no-session retry %d: resumed=%v err=%v", attempt, resumed, err)
		}
	}
	if _, err := os.Stat(txPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rolled-back transaction evidence remains after no-session retry: %v", err)
	}
	if _, exists, err := newNetworkSessionResumeDiagnosticStore(stateStore.runtimeDir, stateStore.readBootID).Load(); err != nil || exists {
		t.Fatalf("replay diagnostic remains after no-session retry: exists=%v err=%v", exists, err)
	}
	if summaries, warnings := txstate.ScanTransactions(stateStore.runtimeDir); len(summaries) != 0 || len(warnings) != 0 {
		t.Fatalf("no-session retry left observable transaction state: summaries=%#v warnings=%#v", summaries, warnings)
	}
}
