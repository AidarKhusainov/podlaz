package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestResumeNetworkSessionDoesNotReadmitIncompleteReplay(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} }
	lifecycle := &scriptedReplayEvidenceLifecycle{errs: []error{
		withTunFailurePhase("network-apply", "tun-incomplete-replay", "completed", errors.New("unsupported replay failure")),
	}}

	if resumed, err := resumeNetworkSession(context.Background(), continuation, lifecycle, inactiveNetworkSessionStatus, successfulNetworkSessionRecovery); err == nil || resumed {
		t.Fatalf("first replay must fail incomplete: resumed=%v err=%v", resumed, err)
	}
	first := loadReplayEvidenceDiagnostic(t, continuation)
	if first.Current == nil || first.Current.ReplayDisposition != networkSessionReplayDispositionIncomplete {
		t.Fatalf("first replay evidence=%#v", first.Current)
	}

	if resumed, err := resumeNetworkSession(context.Background(), continuation, lifecycle, inactiveNetworkSessionStatus, successfulNetworkSessionRecovery); err == nil || resumed {
		t.Fatalf("incomplete replay must remain blocked: resumed=%v err=%v", resumed, err)
	}
	state := loadRecoveryEpochState(t, continuation.stateStore())
	if state.RecoveryEpoch != 1 {
		t.Fatalf("incomplete replay was readmitted: recovery epoch=%d want=1", state.RecoveryEpoch)
	}
	if lifecycle.attempts != 1 {
		t.Fatalf("incomplete replay called Connect again: attempts=%d want=1", lifecycle.attempts)
	}
	current := loadReplayEvidenceDiagnostic(t, continuation).Current
	if current == nil || current.RecoveryEpoch != 1 || current.ReplayDisposition != networkSessionReplayDispositionIncomplete {
		t.Fatalf("incomplete replay evidence changed without admission: %#v", current)
	}
}
