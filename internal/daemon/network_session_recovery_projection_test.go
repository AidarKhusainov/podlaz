package daemon

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestNetworkSessionRecoveryPlanProjectsCurrentReplayDispositionAndApplySubphase(t *testing.T) {
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
		SessionID:            state.SessionID,
		RecoveryEpoch:        state.RecoveryEpoch,
		ReplayDisposition:    networkSessionReplayDispositionTerminal,
		ResumeStage:          api.NetworkSessionResumeStageConnectReplay,
		TUNFailurePhase:      "network-apply",
		NetworkApplySubphase: "dns",
		RollbackStatus:       "completed",
		TransactionPresent:   true,
		CandidateMutation:    networkSessionCandidateMutationRolledBack,
	}
	record := networkSessionResumeDiagnostic{
		RecoveryEpoch:        state.RecoveryEpoch,
		ResumeStage:          api.NetworkSessionResumeStageConnectReplay,
		LastResumeOutcome:    api.NetworkSessionResumeOutcomeFailed,
		TUNFailurePhase:      "network-apply",
		NetworkApplySubphase: "dns",
		RollbackStatus:       "completed",
		TransactionPresent:   true,
	}
	if err := newNetworkSessionResumeDiagnosticStore(runtimeDir, fixedBootID("boot-a")).SaveReplayFailure(record, attempt); err != nil {
		t.Fatal(err)
	}
	gate := newNetworkSessionStartupMutationGate(networkSessionRecordingLifecycle{events: &[]string{}})
	gate.Block()

	plan, err := inspectNetworkSessionRecoveryPlan(continuation, gate)
	if err != nil || plan == nil {
		t.Fatalf("inspect replay projection: plan=%#v err=%v", plan, err)
	}
	if plan.ReplayDisposition != "terminal" || plan.NetworkApplySubphase != "dns" {
		t.Fatalf("replay projection=%#v", plan)
	}
}

func TestNetworkSessionRecoveryPlanClearsStaleReplayProjectionForNewerBlocker(t *testing.T) {
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
		SessionID:            state.SessionID,
		RecoveryEpoch:        state.RecoveryEpoch,
		ReplayDisposition:    networkSessionReplayDispositionTerminal,
		ResumeStage:          api.NetworkSessionResumeStageConnectReplay,
		TUNFailurePhase:      "network-apply",
		NetworkApplySubphase: "dns",
		RollbackStatus:       "completed",
		TransactionPresent:   true,
		CandidateMutation:    networkSessionCandidateMutationRolledBack,
	}
	diagnostics := newNetworkSessionResumeDiagnosticStore(runtimeDir, fixedBootID("boot-a"))
	if err := diagnostics.SaveReplayFailure(networkSessionResumeDiagnostic{
		RecoveryEpoch:        state.RecoveryEpoch,
		ResumeStage:          api.NetworkSessionResumeStageConnectReplay,
		LastResumeOutcome:    api.NetworkSessionResumeOutcomeFailed,
		TUNFailurePhase:      "network-apply",
		NetworkApplySubphase: "dns",
		RollbackStatus:       "completed",
		TransactionPresent:   true,
	}, attempt); err != nil {
		t.Fatal(err)
	}
	if err := diagnostics.SaveLatestBlocker(networkSessionResumeDiagnostic{
		RecoveryEpoch:      state.RecoveryEpoch,
		ResumeStage:        api.NetworkSessionResumeStageExactRecovery,
		LastResumeOutcome:  api.NetworkSessionResumeOutcomeIncomplete,
		TransactionPresent: true,
	}); err != nil {
		t.Fatal(err)
	}
	gate := newNetworkSessionStartupMutationGate(networkSessionRecordingLifecycle{events: &[]string{}})
	gate.Block()

	plan, err := inspectNetworkSessionRecoveryPlan(continuation, gate)
	if err != nil || plan == nil {
		t.Fatalf("inspect newer blocker: plan=%#v err=%v", plan, err)
	}
	if plan.ReplayDisposition != "" || plan.NetworkApplySubphase != "" {
		t.Fatalf("newer non-replay blocker leaked stale replay fields: %#v", plan)
	}
}
