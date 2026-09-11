package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestApplyNetworkSessionResumeResultPreservesPersistedParentDeadlineDisposition(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	state, exists, err := continuation.stateStore().BeginRecoveryAttempt()
	if err != nil || !exists {
		t.Fatalf("begin recovery attempt: exists=%v err=%v", exists, err)
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	replayErr := withTunFailurePhase("network-apply", "tun-parent-deadline", "completed", context.DeadlineExceeded)
	retryErr := persistNetworkSessionReplayFailure(ctx, continuation, state, false, replayErr)
	if retryErr == nil {
		t.Fatal("expected persisted parent-deadline replay failure")
	}

	diagnostic := loadReplayEvidenceDiagnostic(t, continuation)
	if diagnostic.Current == nil || diagnostic.Current.ReplayDisposition != networkSessionReplayDispositionInterrupted {
		t.Fatalf("durable replay disposition=%#v, want interrupted", diagnostic.Current)
	}

	gate := newNetworkSessionStartupMutationGate(networkSessionRecordingLifecycle{events: &[]string{}})
	gate.Block()
	plan, err := inspectNetworkSessionRecoveryPlan(continuation, gate)
	if err != nil || plan == nil {
		t.Fatalf("inspect interrupted replay plan: plan=%#v err=%v", plan, err)
	}
	if plan.ReplayDisposition != api.NetworkSessionReplayDispositionInterrupted || plan.NextAction != api.NetworkSessionRecoveryActionRetryResume {
		t.Fatalf("durable recovery plan=%#v, want interrupted/retry-resume", plan)
	}

	got := applyNetworkSessionResumeResult(
		api.RecoveryResponse{Mode: "execute", NetworkSession: plan},
		gate,
		networkSessionResumeUnknown,
		retryErr,
	)
	if got.NetworkSession == nil {
		t.Fatal("failed replay response lost Network Session projection")
	}
	if got.NetworkSession.ReplayDisposition != api.NetworkSessionReplayDispositionInterrupted || got.NetworkSession.NextAction != api.NetworkSessionRecoveryActionRetryResume {
		t.Fatalf("response replay projection=%#v, want interrupted/retry-resume", got.NetworkSession)
	}
}
