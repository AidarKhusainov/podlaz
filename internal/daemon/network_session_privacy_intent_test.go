package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestNetworkSessionConnectFailureDisarmsResumeIntentWhenProtectionCleanupRemains(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	stateStore := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	inner := protectionLeavingConnectFailureLifecycle{store: stateStore}
	lifecycle := newNetworkSessionLifecycle(inner, continuation)

	_, err := lifecycle.Connect(context.Background(), testContinuationRequest())
	if err == nil {
		t.Fatal("expected protected connect failure")
	}
	if _, ok, loadErr := continuation.LoadCurrent(); loadErr != nil || ok {
		t.Fatalf("failed initial connect must not retain automatic resume intent, ok=%v err=%v", ok, loadErr)
	}
	state, exists, loadErr := stateStore.Load()
	if loadErr != nil || !exists {
		t.Fatalf("failed cleanup must retain durable session authority, exists=%v err=%v", exists, loadErr)
	}
	if state.Intent != networkSessionIntentDisconnect {
		t.Fatalf("failed initial connect intent = %q, want disconnect", state.Intent)
	}
	if state.Protection == nil || state.Protection.State != networkSessionProtectionArmed {
		t.Fatalf("failed initial connect lost privacy cleanup authority: %#v", state.Protection)
	}
}

type protectionLeavingConnectFailureLifecycle struct {
	store networkSessionStateStore
}

func (l protectionLeavingConnectFailureLifecycle) Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
	protection := testArmedPrivacyProtection()
	if err := l.store.SetProtection(&protection); err != nil {
		return api.LifecycleResponse{}, err
	}
	return api.LifecycleResponse{}, errors.New("injected connect failure after protection")
}

func (protectionLeavingConnectFailureLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{}, nil
}
