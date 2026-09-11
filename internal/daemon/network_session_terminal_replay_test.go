package daemon

import (
	"context"
	"errors"
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
		SessionID:          attemptState.SessionID,
		RecoveryEpoch:      attemptState.RecoveryEpoch,
		ReplayDisposition:  networkSessionReplayDispositionTerminal,
		ResumeStage:        api.NetworkSessionResumeStageConnectReplay,
		TUNFailurePhase:    "network-apply",
		RollbackStatus:     "completed",
		TransactionPresent: true,
		TransactionID:      "tun-terminal-replay",
		CandidateMutation:  networkSessionCandidateMutationRolledBack,
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
	continuation.continueTeardown = terminalReplayTestTeardown(t, &terminalConverged)
	lifecycle := &scriptedReplayEvidenceLifecycle{}

	resumed, err := resumeNetworkSessionWithTerminalObservation(
		context.Background(),
		continuation,
		lifecycle,
		inactiveNetworkSessionStatus,
		successfulNetworkSessionRecovery,
		func(context.Context, string) error { return nil },
	)
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

func TestResumeNetworkSessionConvergesNewTerminalReplayInSameAttempt(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	continuation := newNetworkSessionContinuationStore(store.runtimeDir, fixedBootID("boot-a"))
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} }
	terminalConverged := false
	continuation.continueTeardown = terminalReplayTestTeardown(t, &terminalConverged)

	cause := withTunFailurePhase("network-apply", "tun-terminal-replay", "completed", errors.New("typed terminal replay failure"))
	lifecycle := &scriptedReplayEvidenceLifecycle{errs: []error{
		withNetworkSessionReplaySemantics(networkSessionReplayDispositionTerminal, networkSessionCandidateMutationRolledBack, cause),
	}}

	resumed, err := resumeNetworkSessionWithTerminalObservation(
		context.Background(),
		continuation,
		lifecycle,
		inactiveNetworkSessionStatus,
		successfulNetworkSessionRecovery,
		func(context.Context, string) error { return nil },
	)
	if err != nil || resumed {
		t.Fatalf("converge new terminal replay: resumed=%v err=%v", resumed, err)
	}
	if lifecycle.attempts != 1 {
		t.Fatalf("terminal replay attempts=%d want=1", lifecycle.attempts)
	}
	if !terminalConverged {
		t.Fatal("new terminal replay did not enter terminal convergence")
	}
	if _, exists, err := store.Load(); err != nil || exists {
		t.Fatalf("terminal replay convergence left session authority: exists=%v err=%v", exists, err)
	}
	if _, exists, err := newNetworkSessionResumeDiagnosticStore(store.runtimeDir, store.readBootID).Load(); err != nil || exists {
		t.Fatalf("terminal replay convergence left diagnostic evidence: exists=%v err=%v", exists, err)
	}
}

func TestResumeNetworkSessionTerminalObservationFailureKeepsProtectionArmed(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	continuation := newNetworkSessionContinuationStore(store.runtimeDir, fixedBootID("boot-a"))
	attemptState, exists, err := store.BeginRecoveryAttempt()
	if err != nil || !exists {
		t.Fatalf("admit replay attempt: exists=%v err=%v", exists, err)
	}
	attempt := networkSessionReplayAttempt{
		SessionID:          attemptState.SessionID,
		RecoveryEpoch:      attemptState.RecoveryEpoch,
		ReplayDisposition:  networkSessionReplayDispositionTerminal,
		ResumeStage:        api.NetworkSessionResumeStageConnectReplay,
		TUNFailurePhase:    "network-apply",
		RollbackStatus:     "completed",
		TransactionPresent: true,
		TransactionID:      "tun-terminal-replay",
		CandidateMutation:  networkSessionCandidateMutationRolledBack,
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
	lifecycle := &scriptedReplayEvidenceLifecycle{}

	resumed, err := resumeNetworkSessionWithTerminalObservation(
		context.Background(),
		continuation,
		lifecycle,
		inactiveNetworkSessionStatus,
		successfulNetworkSessionRecovery,
		func(context.Context, string) error { return context.DeadlineExceeded },
	)
	if err == nil || resumed {
		t.Fatalf("terminal observation failure: resumed=%v err=%v", resumed, err)
	}
	if lifecycle.attempts != 0 {
		t.Fatalf("terminal replay was readmitted after failed observation: attempts=%d", lifecycle.attempts)
	}
	state, exists, loadErr := store.Load()
	if loadErr != nil || !exists {
		t.Fatalf("load protected session after failed observation: exists=%v err=%v", exists, loadErr)
	}
	if state.Intent != networkSessionIntentResume || state.Protection == nil {
		t.Fatalf("failed observation changed terminal authority: %#v", state)
	}
}

func TestResumeNetworkSessionDoesNotInferTerminalityFromCleanRecoveryWithoutReplayEvidence(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	continuation := newNetworkSessionContinuationStore(store.runtimeDir, fixedBootID("boot-a"))
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} }
	lifecycle := &scriptedReplayEvidenceLifecycle{}

	resumed, err := resumeNetworkSessionWithTerminalObservation(
		context.Background(),
		continuation,
		lifecycle,
		inactiveNetworkSessionStatus,
		successfulNetworkSessionRecovery,
		func(context.Context, string) error { return nil },
	)
	if err == nil || resumed {
		t.Fatalf("clean recovery without evidence: resumed=%v err=%v", resumed, err)
	}
	state, exists, loadErr := store.Load()
	if loadErr != nil || !exists {
		t.Fatalf("load session after clean recovery: exists=%v err=%v", exists, loadErr)
	}
	if state.Intent != networkSessionIntentResume || state.Protection == nil {
		t.Fatalf("clean recovery without evidence terminalized session: %#v", state)
	}
}

func terminalReplayTestTeardown(t *testing.T, converged *bool) networkSessionTeardownRecoveryStage {
	t.Helper()
	return func(_ context.Context, current networkSessionStateStore) error {
		state, exists, err := current.Load()
		if err != nil || !exists {
			t.Fatalf("load terminalized session: exists=%v err=%v", exists, err)
		}
		if state.Intent != networkSessionIntentTerminal {
			t.Fatalf("terminal replay intent=%q want=%q", state.Intent, networkSessionIntentTerminal)
		}
		*converged = true
		if err := current.SetProtection(nil); err != nil {
			return err
		}
		return current.Remove()
	}
}
