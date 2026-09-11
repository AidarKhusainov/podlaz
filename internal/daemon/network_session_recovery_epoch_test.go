package daemon

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestResumeNetworkSessionDoesNotAdvanceRecoveryEpochForPreReplayBlocker(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error {
		return errors.New("privacy reconciliation blocked")
	}

	resumed, err := resumeNetworkSession(
		context.Background(),
		continuation,
		&recoveryEpochRecordingLifecycle{store: continuation.stateStore()},
		inactiveNetworkSessionStatus,
		successfulNetworkSessionRecovery,
	)
	if err == nil || resumed {
		t.Fatalf("pre-replay blocker: resumed=%v err=%v", resumed, err)
	}

	state := loadRecoveryEpochState(t, continuation.stateStore())
	if state.RecoveryEpoch != 0 {
		t.Fatalf("pre-replay blocker advanced recovery epoch: got=%d want=0", state.RecoveryEpoch)
	}
}

func TestResumeNetworkSessionAdvancesRecoveryEpochImmediatelyBeforeReplay(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	store := continuation.stateStore()
	events := make([]string, 0, 4)
	recordEpoch := func(stage string) {
		state := loadRecoveryEpochState(t, store)
		events = append(events, fmt.Sprintf("%s:%d", stage, state.RecoveryEpoch))
	}
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error {
		recordEpoch("privacy")
		return nil
	}
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
		recordEpoch("exact")
		return api.RecoveryResponse{Mode: "execute"}
	}
	lifecycle := &recoveryEpochRecordingLifecycle{store: store, onConnect: func(epoch uint64) {
		events = append(events, fmt.Sprintf("connect:%d", epoch))
	}}
	recover := func(context.Context, api.StatusResponse) api.RecoveryResponse {
		recordEpoch("generic")
		return api.RecoveryResponse{Mode: "execute"}
	}

	resumed, err := resumeNetworkSession(context.Background(), continuation, lifecycle, inactiveNetworkSessionStatus, recover)
	if err != nil || !resumed {
		t.Fatalf("resume: resumed=%v err=%v", resumed, err)
	}
	want := []string{"privacy:0", "exact:0", "generic:0", "connect:1"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("recovery admission order=%v want=%v", events, want)
	}
	if state := loadRecoveryEpochState(t, store); state.RecoveryEpoch != 1 {
		t.Fatalf("recovery epoch=%d want=1", state.RecoveryEpoch)
	}
}

func TestResumeNetworkSessionAbandonedAdmissionRecoversBeforeNewEpoch(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	store := continuation.stateStore()
	abandoned, exists, err := store.BeginRecoveryAttempt()
	if err != nil || !exists {
		t.Fatalf("persist abandoned admission: exists=%v err=%v", exists, err)
	}
	if abandoned.RecoveryEpoch != 1 {
		t.Fatalf("abandoned recovery epoch=%d want=1", abandoned.RecoveryEpoch)
	}

	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error { return nil }
	exactCalls := 0
	observedExactEpochs := make([]uint64, 0, 2)
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
		exactCalls++
		observedExactEpochs = append(observedExactEpochs, loadRecoveryEpochState(t, store).RecoveryEpoch)
		if exactCalls == 1 {
			return api.RecoveryResponse{
				Mode:     "execute",
				Warnings: []api.RecoveryWarning{{Target: "transaction state", Message: "exact recovery incomplete"}},
			}
		}
		return api.RecoveryResponse{Mode: "execute"}
	}
	lifecycle := &recoveryEpochRecordingLifecycle{store: store}

	if resumed, err := resumeNetworkSession(context.Background(), continuation, lifecycle, inactiveNetworkSessionStatus, successfulNetworkSessionRecovery); err == nil || resumed {
		t.Fatalf("incomplete exact recovery must block abandoned replay: resumed=%v err=%v", resumed, err)
	}
	if state := loadRecoveryEpochState(t, store); state.RecoveryEpoch != 1 {
		t.Fatalf("incomplete exact recovery advanced epoch: got=%d want=1", state.RecoveryEpoch)
	}
	if lifecycle.connectEpochs != nil {
		t.Fatalf("Connect ran before exact recovery converged: epochs=%v", lifecycle.connectEpochs)
	}

	if resumed, err := resumeNetworkSession(context.Background(), continuation, lifecycle, inactiveNetworkSessionStatus, successfulNetworkSessionRecovery); err != nil || !resumed {
		t.Fatalf("fresh entry after exact recovery must replay: resumed=%v err=%v", resumed, err)
	}
	if !reflect.DeepEqual(observedExactEpochs, []uint64{1, 1}) {
		t.Fatalf("exact recovery observed epochs=%v want=[1 1]", observedExactEpochs)
	}
	if !reflect.DeepEqual(lifecycle.connectEpochs, []uint64{2}) {
		t.Fatalf("Connect epochs=%v want=[2]", lifecycle.connectEpochs)
	}
}

func loadRecoveryEpochState(t *testing.T, store networkSessionStateStore) networkSessionState {
	t.Helper()
	state, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("load Network Session state: exists=%v err=%v", exists, err)
	}
	return state
}

type recoveryEpochRecordingLifecycle struct {
	store         networkSessionStateStore
	connectEpochs []uint64
	onConnect     func(uint64)
}

func (l *recoveryEpochRecordingLifecycle) Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
	state, exists, err := l.store.Load()
	if err != nil {
		return api.LifecycleResponse{}, err
	}
	if !exists {
		return api.LifecycleResponse{}, errors.New("Network Session disappeared before replay")
	}
	l.connectEpochs = append(l.connectEpochs, state.RecoveryEpoch)
	if l.onConnect != nil {
		l.onConnect(state.RecoveryEpoch)
	}
	return api.LifecycleResponse{Connection: "active", Mode: state.Request.Mode}, nil
}

func (*recoveryEpochRecordingLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive"}, nil
}
