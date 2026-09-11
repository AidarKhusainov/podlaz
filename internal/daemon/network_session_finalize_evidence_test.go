package daemon

import (
	"os"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestFinalizeRemovesRolledBackReplayEvidenceThenSession(t *testing.T) {
	stateStore := seededProtectedNetworkSessionStore(t, networkSessionIntentTerminal)
	continuation := newNetworkSessionContinuationStore(stateStore.runtimeDir, fixedBootID("boot-a"))
	state, exists, err := stateStore.Load()
	if err != nil || !exists {
		t.Fatalf("load session: exists=%v err=%v", exists, err)
	}

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
	stateStore := seededProtectedNetworkSessionStore(t, networkSessionIntentTerminal)
	continuation := newNetworkSessionContinuationStore(stateStore.runtimeDir, fixedBootID("boot-a"))
	state, exists, err := stateStore.Load()
	if err != nil || !exists {
		t.Fatalf("load session: exists=%v err=%v", exists, err)
	}

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
