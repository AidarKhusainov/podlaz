package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestNetworkSessionLifecycleFailedProtectedReplacementReconnectsPreviousDataPlane(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	previous := testContinuationRequest()
	if err := continuation.Save(previous); err != nil {
		t.Fatal(err)
	}
	protection := testArmedPrivacyProtection()
	if err := continuation.stateStore().SetProtection(&protection); err != nil {
		t.Fatal(err)
	}

	target := previous
	target.Handoff = api.HandoffReplacePodlaz
	target.Profile.ID = "profile-replacement"
	target.Profile.Name = "Replacement profile"
	target.Profile.Server = "replacement.example.test"

	inner := &replacementRestoringLifecycle{
		store:     continuation.stateStore(),
		targetID:  target.Profile.ID,
		targetErr: errors.New("synthetic target generation failure"),
	}
	lifecycle := newNetworkSessionLifecycle(inner, continuation)

	_, err := lifecycle.Connect(context.Background(), target)
	if !errors.Is(err, inner.targetErr) {
		t.Fatalf("replacement error=%v, want target failure", err)
	}
	wantCalls := []string{"profile-replacement", "profile-example"}
	if !reflect.DeepEqual(inner.calls, wantCalls) {
		t.Fatalf("inner connect calls=%#v, want target then previous restore %#v", inner.calls, wantCalls)
	}
	state, exists, loadErr := continuation.stateStore().Load()
	if loadErr != nil || !exists {
		t.Fatalf("load restored session: exists=%v err=%v", exists, loadErr)
	}
	if state.Replacement != nil || state.Request.Profile.ID != previous.Profile.ID {
		t.Fatalf("previous session authority not restored: %#v", state)
	}
	if state.Protection == nil || state.Protection.State != networkSessionProtectionArmed {
		t.Fatalf("previous privacy barrier not retained: %#v", state.Protection)
	}
}

func TestNetworkSessionLifecycleFailedProtectedReplacementRestoresPreviousDataPlaneAfterCallerCancellation(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	previous := testContinuationRequest()
	if err := continuation.Save(previous); err != nil {
		t.Fatal(err)
	}
	protection := testArmedPrivacyProtection()
	if err := continuation.stateStore().SetProtection(&protection); err != nil {
		t.Fatal(err)
	}

	target := previous
	target.Handoff = api.HandoffReplacePodlaz
	target.Profile.ID = "profile-replacement"
	target.Profile.Name = "Replacement profile"
	target.Profile.Server = "replacement.example.test"

	ctx, cancel := context.WithCancel(context.Background())
	inner := &replacementRestoringLifecycle{
		store:        continuation.stateStore(),
		targetID:     target.Profile.ID,
		targetErr:    context.Canceled,
		cancelTarget: cancel,
	}
	lifecycle := newNetworkSessionLifecycle(inner, continuation)

	_, err := lifecycle.Connect(ctx, target)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("replacement error=%v, want caller cancellation", err)
	}
	if inner.previousContextErr != nil {
		t.Fatalf("previous data-plane restore inherited caller cancellation: %v", inner.previousContextErr)
	}
	if !inner.previousHasDeadline {
		t.Fatal("previous data-plane restore must use a bounded daemon-owned context")
	}
	wantCalls := []string{"profile-replacement", "profile-example"}
	if !reflect.DeepEqual(inner.calls, wantCalls) {
		t.Fatalf("inner connect calls=%#v, want target then previous restore %#v", inner.calls, wantCalls)
	}
}

type replacementRestoringLifecycle struct {
	store              networkSessionStateStore
	targetID           string
	targetErr          error
	cancelTarget       context.CancelFunc
	previousContextErr error
	previousHasDeadline bool
	calls              []string
}

func (l *replacementRestoringLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	l.calls = append(l.calls, request.Profile.ID)
	if request.Profile.ID == l.targetID {
		// Production connectTun restores the previous barrier/request before
		// returning a target-generation failure. Model that inner guarantee here
		// so this test isolates the outer lifecycle's responsibility to restore
		// the previous Data Plane Generation as well.
		if err := l.store.RestoreReplacement(); err != nil {
			return api.LifecycleResponse{}, err
		}
		if l.cancelTarget != nil {
			l.cancelTarget()
		}
		return api.LifecycleResponse{}, l.targetErr
	}
	l.previousContextErr = ctx.Err()
	_, l.previousHasDeadline = ctx.Deadline()
	if l.previousContextErr != nil {
		return api.LifecycleResponse{}, l.previousContextErr
	}
	return api.LifecycleResponse{Connection: "active", Proxy: "inactive", TUN: "active"}, nil
}

func (l *replacementRestoringLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
}
