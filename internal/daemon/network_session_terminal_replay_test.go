package daemon

import (
	"context"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestResumeNetworkSessionConsumesCurrentTerminalReplayWithoutReadmission(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	continuation := newNetworkSessionContinuationStore(store.runtimeDir, fixedBootID("boot-a"))
	attemptState, exists, err := store.BeginRecoveryAttempt()
	if err != nil || !exists {
		t.Fatalf("admit replay attempt: exists=%v err=%v", exists, err)
	}
	attempt := networkSessionReplayAttempt{
		SessionID:            attemptState.SessionID,
		RecoveryEpoch:        attemptState.RecoveryEpoch,
		ReplayDisposition:    networkSessionReplayDispositionTerminal,
		ResumeStage:          api.NetworkSessionResumeStageConnectReplay,
		TUNFailurePhase:      "network-apply",
		RollbackStatus:       "completed",
		TransactionPresent:   true,
		CandidateMutation:    networkSessionCandidateMutationRolledBack,
	}
	record := networkSessionResumeDiagnostic{
		RecoveryEpoch:      attemptState.RecoveryEpoch,
		ResumeStage:        api.NetworkSessionResumeStageConnectReplay,
		LastResumeOutcome:  api.NetworkSessionResumeOutcomeFailed,
		TUNFailurePhase:    "network-apply",
		RollbackStatus:     "completed",
		TransactionPresent: true,
	}
	if err := newNetworkSessionResumeDiagnosticStore(store.runtimeDir, store.readBootID).SaveReplayFailure(record, attempt); err != nil {
		t.Fatal(err)
	}

	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} }
	terminalConverged := false
	continuation.continueTeardown = func(_ context.Context, current networkSessionStateStore) error {
		state, exists, err := current.Load()
		if err != nil || !exists {
			t.Fatalf("load terminalized session: exists=%v err=%v", exists, err)
		}
		if state.Intent != networkSessionIntentTerminal {
			t.Fatalf("terminal replay intent=%q want=%q", state.Intent, networkSessionIntentTerminal)
		}
		terminalConverged = true
		if err := current.SetProtection(nil); err != nil {
			return err
		}
		return current.Remove()
	}
	lifecycle := &scriptedReplayEvidenceLifecycle{}

	resumed, err := resumeNetworkSession(context.Background(), continuation, lifecycle, inactiveNetworkSessionStatus, successfulNetworkSessionRecovery)
	if err != nil || resumed {
		t.Fatalf("consume terminal replay: resumed=%v err=%v", resumed, err)
	}
	if lifecycle.attempts != 0 {
		t.Fatalf("terminal replay was readmitted: attempts=%d", lifecycle.attempts)
	}
	if !terminalConverged {
		t.Fatal("terminal replay did not enter terminal convergence")
	}
	if _, exists, err := store.Load(); err != nil || exists {
		t.Fatalf("terminal replay convergence left session authority: exists=%v err=%v", exists, err)
	}
}
