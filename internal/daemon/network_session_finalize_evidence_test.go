package daemon

import (
	"os"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestFinalizeRemovesRolledBackReplayEvidenceThenSession(t *testing.T) {
	stateStore, state := admittedTerminalSessionForFinalizeTest(t)
	continuation := newNetworkSessionContinuationStore(stateStore.runtimeDir, fixedBootID("boot-a"))

	txStore := txstate.TransactionStore{RuntimeDir: stateStore.runtimeDir}
	tx := txstate.NewTransaction("tun-finalize", "profile-test", "tun", time.Now().UTC())
	tx.State = txstate.TransactionRolledBack
	path, err := txStore.Save(tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveFinalizeReplayDiagnostic(stateStore, state, tx.ID); err != nil {
		t.Fatal(err)
	}

	if err := continuation.finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("retained transaction remains: %v", err)
	}
	if _, exists, err := newNetworkSessionResumeDiagnosticStore(stateStore.runtimeDir, stateStore.readBootID).Load(); err != nil || exists {
		t.Fatalf("diagnostic remains: exists=%v err=%v", exists, err)
	}
	if _, exists, err := stateStore.Load(); err != nil || exists {
		t.Fatalf("session remains: exists=%v err=%v", exists, err)
	}
}

func TestFinalizeKeepsSessionWhenReferencedTransactionStillNeedsRecovery(t *testing.T) {
	stateStore, state := admittedTerminalSessionForFinalizeTest(t)
	continuation := newNetworkSessionContinuationStore(stateStore.runtimeDir, fixedBootID("boot-a"))

	txStore := txstate.TransactionStore{RuntimeDir: stateStore.runtimeDir}
	tx := txstate.NewTransaction("tun-finalize-blocked", "profile-test", "tun", time.Now().UTC())
	tx.State = txstate.TransactionFailed
	path, err := txStore.Save(tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveFinalizeReplayDiagnostic(stateStore, state, tx.ID); err != nil {
		t.Fatal(err)
	}

	if err := continuation.finalize(); err == nil {
		t.Fatal("expected finalization to reject recovery-required transaction")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("referenced transaction was removed: %v", err)
	}
	if _, exists, err := stateStore.Load(); err != nil || !exists {
		t.Fatalf("session authority was removed: exists=%v err=%v", exists, err)
	}
}

func admittedTerminalSessionForFinalizeTest(t *testing.T) (networkSessionStateStore, networkSessionState) {
	t.Helper()
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	state, exists, err := store.BeginRecoveryAttempt()
	if err != nil || !exists {
		t.Fatalf("begin recovery attempt: exists=%v err=%v", exists, err)
	}
	if err := store.SetIntent(networkSessionIntentTerminal); err != nil {
		t.Fatalf("set terminal intent: %v", err)
	}
	state, exists, err = store.Load()
	if err != nil || !exists {
		t.Fatalf("load terminal session: exists=%v err=%v", exists, err)
	}
	return store, state
}

func saveFinalizeReplayDiagnostic(store networkSessionStateStore, state networkSessionState, transactionID string) error {
	attempt := networkSessionReplayAttempt{
		SessionID:          state.SessionID,
		RecoveryEpoch:      state.RecoveryEpoch,
		ReplayDisposition:  networkSessionReplayDispositionTerminal,
		ResumeStage:        api.NetworkSessionResumeStageConnectReplay,
		TUNFailurePhase:    "network-apply",
		RollbackStatus:     "completed",
		TransactionPresent: true,
		TransactionID:      transactionID,
		CandidateMutation:  networkSessionCandidateMutationRolledBack,
	}
	return newNetworkSessionResumeDiagnosticStore(store.runtimeDir, store.readBootID).SaveReplayFailure(
		networkSessionResumeDiagnostic{
			RecoveryEpoch:      state.RecoveryEpoch,
			ResumeStage:        api.NetworkSessionResumeStageConnectReplay,
			LastResumeOutcome:  api.NetworkSessionResumeOutcomeFailed,
			TUNFailurePhase:    "network-apply",
			RollbackStatus:     "completed",
			TransactionPresent: true,
		},
		attempt,
	)
}
