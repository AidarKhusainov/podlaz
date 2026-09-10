package daemon

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestRecoveryPlanUsesReplayDispositionForNextAction(t *testing.T) {
	tests := []struct {
		name        string
		disposition networkSessionReplayDisposition
		want        string
	}{
		{name: "terminal", disposition: networkSessionReplayDispositionTerminal, want: api.NetworkSessionRecoveryActionContinueTeardown},
		{name: "incomplete", disposition: networkSessionReplayDispositionIncomplete, want: api.NetworkSessionRecoveryActionManualDiagnosis},
		{name: "retryable", disposition: networkSessionReplayDispositionRetryable, want: api.NetworkSessionRecoveryActionRetryResume},
		{name: "interrupted", disposition: networkSessionReplayDispositionInterrupted, want: api.NetworkSessionRecoveryActionRetryResume},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
			if err := continuation.Save(testContinuationRequest()); err != nil {
				t.Fatal(err)
			}
			state, exists, err := continuation.stateStore().BeginRecoveryAttempt()
			if err != nil || !exists {
				t.Fatalf("begin replay attempt: exists=%v err=%v", exists, err)
			}
			attempt := networkSessionReplayAttempt{
				SessionID:          state.SessionID,
				RecoveryEpoch:      state.RecoveryEpoch,
				ReplayDisposition:  tt.disposition,
				ResumeStage:        api.NetworkSessionResumeStageConnectReplay,
				TUNFailurePhase:    "network-apply",
				RollbackStatus:     "completed",
				TransactionPresent: true,
				CandidateMutation:  networkSessionCandidateMutationRolledBack,
			}
			record := networkSessionResumeDiagnostic{
				RecoveryEpoch:      state.RecoveryEpoch,
				ResumeStage:        api.NetworkSessionResumeStageConnectReplay,
				LastResumeOutcome:  api.NetworkSessionResumeOutcomeFailed,
				TUNFailurePhase:    "network-apply",
				RollbackStatus:     "completed",
				TransactionPresent: true,
			}
			if err := newNetworkSessionResumeDiagnosticStore(runtimeDir, fixedBootID("boot-a")).SaveReplayFailure(record, attempt); err != nil {
				t.Fatal(err)
			}
			gate := newNetworkSessionStartupMutationGate(networkSessionRecordingLifecycle{events: &[]string{}})
			gate.Block()

			plan, err := inspectNetworkSessionRecoveryPlan(continuation, gate)
			if err != nil || plan == nil {
				t.Fatalf("inspect recovery plan: plan=%#v err=%v", plan, err)
			}
			if plan.NextAction != tt.want {
				t.Fatalf("next_action=%q, want %q for disposition %q", plan.NextAction, tt.want, tt.disposition)
			}
		})
	}
}
