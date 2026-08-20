package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestPersistedTerminalArmedProtectionStaysUpThroughDataPlaneRecovery(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentTerminal)
	continuation := newNetworkSessionContinuationStore(store.runtimeDir, fixedBootID("boot-a"))
	events := []string{}
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
		assertProtectionState(t, store, networkSessionProtectionArmed)
		events = append(events, "exact-data-plane-recovery")
		return api.RecoveryResponse{Mode: "execute"}
	}
	executor := &privacyEnvelopeExecutorStub{exists: true}
	continuation.continueTeardown = func(ctx context.Context, stateStore networkSessionStateStore) error {
		events = append(events, "terminal-envelope-convergence")
		return continuePersistedNetworkSessionTeardownWith(ctx, stateStore, executor, func(context.Context) error {
			events = append(events, "post-network-verify")
			return nil
		})
	}

	resumed, err := resumeNetworkSession(
		context.Background(),
		continuation,
		networkSessionRecordingLifecycle{events: &events},
		func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
		func(context.Context, api.StatusResponse) api.RecoveryResponse {
			assertProtectionState(t, store, networkSessionProtectionArmed)
			events = append(events, "generic-data-plane-recovery")
			return api.RecoveryResponse{Mode: "execute"}
		},
	)
	if err != nil || resumed {
		t.Fatalf("terminal restart convergence: resumed=%v err=%v", resumed, err)
	}
	want := []string{"exact-data-plane-recovery", "generic-data-plane-recovery", "terminal-envelope-convergence", "post-network-verify"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("terminal restart ordering=%#v, want %#v", events, want)
	}
	if _, exists, err := store.Load(); err != nil || exists {
		t.Fatalf("terminal convergence left session authority: exists=%v err=%v", exists, err)
	}
}

func TestPersistedRemovingProtectionConvergesWhenTableStillPresent(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentTerminal)
	setProtectionState(t, store, networkSessionProtectionRemoving)
	executor := &privacyEnvelopeExecutorStub{exists: true}
	verifiedNetwork := false

	if err := continuePersistedNetworkSessionTeardownWith(context.Background(), store, executor, func(context.Context) error {
		verifiedNetwork = true
		return nil
	}); err != nil {
		t.Fatalf("continue removing protection with table present: %v", err)
	}
	if executor.removeCalls != 1 || !verifiedNetwork {
		t.Fatalf("present removing table did not converge exactly: remove=%d verifyNetwork=%v", executor.removeCalls, verifiedNetwork)
	}
	if _, exists, err := store.Load(); err != nil || exists {
		t.Fatalf("converged removing table left authority: exists=%v err=%v", exists, err)
	}
}

func TestPersistedRemovingProtectionConvergesWhenTableAlreadyAbsent(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentDisconnect)
	setProtectionState(t, store, networkSessionProtectionRemoving)
	executor := &privacyEnvelopeExecutorStub{exists: false}

	if err := continuePersistedNetworkSessionTeardownWith(context.Background(), store, executor, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("continue removing protection after nft delete: %v", err)
	}
	if executor.removeCalls != 0 || executor.verifyCalls != 0 {
		t.Fatalf("already absent envelope was mutated: remove=%d verify=%d", executor.removeCalls, executor.verifyCalls)
	}
	if _, exists, err := store.Load(); err != nil || exists {
		t.Fatalf("absent removing table left authority: exists=%v err=%v", exists, err)
	}
}

func TestPersistedProtectionClearedButPostNetworkProofIncompleteRetriesWithoutReconnect(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentTerminal)
	if err := store.SetProtection(nil); err != nil {
		t.Fatal(err)
	}
	probeErr := errors.New("synthetic remaining-network proof failure")
	if err := continuePersistedNetworkSessionTeardownWith(context.Background(), store, &privacyEnvelopeExecutorStub{}, func(context.Context) error {
		return probeErr
	}); !errors.Is(err, probeErr) {
		t.Fatalf("post-network proof failure=%v, want %v", err, probeErr)
	}
	state, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("post-network failure lost terminal authority: exists=%v err=%v", exists, err)
	}
	if state.Intent != networkSessionIntentTerminal || state.Protection != nil {
		t.Fatalf("post-network failure changed terminal authority: %#v", state)
	}

	if err := continuePersistedNetworkSessionTeardownWith(context.Background(), store, &privacyEnvelopeExecutorStub{}, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("retry post-network convergence: %v", err)
	}
	if _, exists, err := store.Load(); err != nil || exists {
		t.Fatalf("post-network retry left session authority: exists=%v err=%v", exists, err)
	}
}

func assertProtectionState(t *testing.T, store networkSessionStateStore, want networkSessionProtectionState) {
	t.Helper()
	state, exists, err := store.Load()
	if err != nil || !exists || state.Protection == nil {
		t.Fatalf("load protection: exists=%v state=%#v err=%v", exists, state, err)
	}
	if state.Protection.State != want {
		t.Fatalf("protection state=%q, want %q", state.Protection.State, want)
	}
}

func setProtectionState(t *testing.T, store networkSessionStateStore, state networkSessionProtectionState) {
	t.Helper()
	current, exists, err := store.Load()
	if err != nil || !exists || current.Protection == nil {
		t.Fatalf("load protection: exists=%v err=%v", exists, err)
	}
	protection := cloneNetworkSessionProtection(*current.Protection)
	protection.State = state
	if err := store.SetProtection(&protection); err != nil {
		t.Fatal(err)
	}
}
