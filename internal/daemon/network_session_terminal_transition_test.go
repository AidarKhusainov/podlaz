package daemon

import "testing"

func TestNetworkSessionTerminalTransitionCommitsOnlyCurrentProvenAttempt(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	state, exists, err := store.BeginRecoveryAttempt()
	if err != nil || !exists {
		t.Fatalf("begin recovery attempt: exists=%v err=%v", exists, err)
	}
	attempt := networkSessionReplayAttempt{
		SessionID:         state.SessionID,
		RecoveryEpoch:     state.RecoveryEpoch,
		ReplayDisposition: networkSessionReplayDispositionTerminal,
		RollbackStatus:    "completed",
		CandidateMutation: networkSessionCandidateMutationRolledBack,
	}
	witness := networkSessionTerminalCleanupWitness{
		sessionID:     state.SessionID,
		recoveryEpoch: state.RecoveryEpoch,
		proven:        true,
	}

	outcome, err := store.TransitionReplayToTerminal(attempt, witness)
	if err != nil {
		t.Fatalf("transition current terminal replay: %v", err)
	}
	if outcome != networkSessionTerminalTransitionCommitted {
		t.Fatalf("transition outcome=%q want=%q", outcome, networkSessionTerminalTransitionCommitted)
	}
	got, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("load terminalized session: exists=%v err=%v", exists, err)
	}
	if got.Intent != networkSessionIntentTerminal || got.Protection == nil {
		t.Fatalf("terminal transition lost intent/protection authority: %#v", got)
	}
}

func TestNetworkSessionTerminalTransitionRejectsStaleOrUnsafeEvidenceWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*networkSessionState, *networkSessionReplayAttempt, *networkSessionTerminalCleanupWitness)
	}{
		{name: "stale-session", mutate: func(_ *networkSessionState, attempt *networkSessionReplayAttempt, _ *networkSessionTerminalCleanupWitness) {
			attempt.SessionID = "ffffffffffffffffffffffffffffffff"
		}},
		{name: "stale-epoch", mutate: func(_ *networkSessionState, attempt *networkSessionReplayAttempt, _ *networkSessionTerminalCleanupWitness) {
			attempt.RecoveryEpoch++
		}},
		{name: "non-terminal-evidence", mutate: func(_ *networkSessionState, attempt *networkSessionReplayAttempt, _ *networkSessionTerminalCleanupWitness) {
			attempt.ReplayDisposition = networkSessionReplayDispositionRetryable
		}},
		{name: "unproven-witness", mutate: func(_ *networkSessionState, _ *networkSessionReplayAttempt, witness *networkSessionTerminalCleanupWitness) {
			witness.proven = false
		}},
		{name: "stale-witness-session", mutate: func(_ *networkSessionState, _ *networkSessionReplayAttempt, witness *networkSessionTerminalCleanupWitness) {
			witness.sessionID = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
		}},
		{name: "stale-witness-epoch", mutate: func(_ *networkSessionState, _ *networkSessionReplayAttempt, witness *networkSessionTerminalCleanupWitness) {
			witness.recoveryEpoch++
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
			state, exists, err := store.BeginRecoveryAttempt()
			if err != nil || !exists {
				t.Fatalf("begin recovery attempt: exists=%v err=%v", exists, err)
			}
			attempt := networkSessionReplayAttempt{
				SessionID:         state.SessionID,
				RecoveryEpoch:     state.RecoveryEpoch,
				ReplayDisposition: networkSessionReplayDispositionTerminal,
				RollbackStatus:    "not-started",
				CandidateMutation: networkSessionCandidateMutationNotOpened,
			}
			witness := networkSessionTerminalCleanupWitness{sessionID: state.SessionID, recoveryEpoch: state.RecoveryEpoch, proven: true}
			tt.mutate(&state, &attempt, &witness)

			outcome, err := store.TransitionReplayToTerminal(attempt, witness)
			if err != nil {
				t.Fatalf("reject stale/unsafe evidence: %v", err)
			}
			if outcome != networkSessionTerminalTransitionRejected {
				t.Fatalf("transition outcome=%q want=%q", outcome, networkSessionTerminalTransitionRejected)
			}
			got, exists, err := store.Load()
			if err != nil || !exists {
				t.Fatalf("load unchanged session: exists=%v err=%v", exists, err)
			}
			if got.Intent != networkSessionIntentResume || got.Protection == nil {
				t.Fatalf("rejected transition mutated authority: %#v", got)
			}
		})
	}
}

func TestNetworkSessionTerminalTransitionRejectsSupersededIntentWithoutMutation(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentDisconnect)
	state, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("load superseded session: exists=%v err=%v", exists, err)
	}
	attempt := networkSessionReplayAttempt{SessionID: state.SessionID, RecoveryEpoch: state.RecoveryEpoch, ReplayDisposition: networkSessionReplayDispositionTerminal, CandidateMutation: networkSessionCandidateMutationNotOpened, RollbackStatus: "not-started"}
	witness := networkSessionTerminalCleanupWitness{sessionID: state.SessionID, recoveryEpoch: state.RecoveryEpoch, proven: true}

	outcome, err := store.TransitionReplayToTerminal(attempt, witness)
	if err != nil {
		t.Fatalf("reject superseded intent: %v", err)
	}
	if outcome != networkSessionTerminalTransitionRejected {
		t.Fatalf("transition outcome=%q want=%q", outcome, networkSessionTerminalTransitionRejected)
	}
	got, exists, err := store.Load()
	if err != nil || !exists || got.Intent != networkSessionIntentDisconnect || got.Protection == nil {
		t.Fatalf("superseded session mutated: exists=%v state=%#v err=%v", exists, got, err)
	}
}
