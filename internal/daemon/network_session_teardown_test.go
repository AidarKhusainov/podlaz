package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestNetworkSessionTeardownOrdersTerminalCleanupBehindPrivacyEnvelope(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	var events []string
	coordinator := networkSessionTeardownCoordinator{
		store: store,
		cleanupDataPlane: func(context.Context) (api.LifecycleResponse, error) {
			state, _, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if state.Intent != networkSessionIntentTerminal || state.Protection == nil {
				t.Fatalf("terminal intent/protection not durable before data-plane cleanup: %#v", state)
			}
			events = append(events, "data-plane-cleanup")
			return api.LifecycleResponse{Connection: "inactive"}, nil
		},
		removeProtection: func(context.Context) error {
			events = append(events, "envelope-remove")
			return store.SetProtection(nil)
		},
		verifyRemainingNetwork: func(context.Context) error {
			events = append(events, "post-network-verify")
			return nil
		},
	}

	_, err := coordinator.Teardown(context.Background(), networkSessionTeardownTerminal)
	if err != nil {
		t.Fatalf("terminal teardown: %v", err)
	}
	want := []string{"data-plane-cleanup", "envelope-remove", "post-network-verify"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("terminal teardown order=%#v, want %#v", events, want)
	}
	if _, exists, err := store.Load(); err != nil || exists {
		t.Fatalf("successful terminal teardown must clear session authority: exists=%v err=%v", exists, err)
	}
}

func TestNetworkSessionTeardownRestartKeepsEnvelopeAndResumeAuthority(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	removeCalls := 0
	verifyCalls := 0
	coordinator := networkSessionTeardownCoordinator{
		store: store,
		cleanupDataPlane: func(context.Context) (api.LifecycleResponse, error) {
			return api.LifecycleResponse{Connection: "inactive"}, nil
		},
		removeProtection:       func(context.Context) error { removeCalls++; return nil },
		verifyRemainingNetwork: func(context.Context) error { verifyCalls++; return nil },
	}

	if _, err := coordinator.Teardown(context.Background(), networkSessionTeardownRestart); err != nil {
		t.Fatalf("restart teardown: %v", err)
	}
	state, exists, err := store.Load()
	if err != nil || !exists {
		t.Fatalf("restart must retain session authority: exists=%v err=%v", exists, err)
	}
	if state.Intent != networkSessionIntentResume || state.Protection == nil {
		t.Fatalf("restart changed protected resume authority: %#v", state)
	}
	if removeCalls != 0 || verifyCalls != 0 {
		t.Fatalf("restart must not remove envelope or verify direct baseline: remove=%d verify=%d", removeCalls, verifyCalls)
	}
}

func TestNetworkSessionTeardownKeepsEnvelopeWhenDataPlaneCleanupFails(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	removeCalls := 0
	coordinator := networkSessionTeardownCoordinator{
		store: store,
		cleanupDataPlane: func(context.Context) (api.LifecycleResponse, error) {
			return api.LifecycleResponse{}, errors.New("synthetic data-plane cleanup failure")
		},
		removeProtection:       func(context.Context) error { removeCalls++; return nil },
		verifyRemainingNetwork: func(context.Context) error { return nil },
	}

	_, err := coordinator.Teardown(context.Background(), networkSessionTeardownExplicit)
	if err == nil {
		t.Fatal("expected data-plane cleanup failure")
	}
	state, exists, loadErr := store.Load()
	if loadErr != nil || !exists || state.Protection == nil {
		t.Fatalf("failed data-plane cleanup lost exact envelope authority: exists=%v state=%#v err=%v", exists, state, loadErr)
	}
	if state.Intent != networkSessionIntentDisconnect {
		t.Fatalf("explicit failure must stay disarmed: intent=%q", state.Intent)
	}
	if removeCalls != 0 {
		t.Fatalf("envelope removed before data-plane cleanup converged: calls=%d", removeCalls)
	}
}

func TestNetworkSessionTeardownPreservesAuthorityOnEnvelopeRemovalFailure(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	verifyCalls := 0
	coordinator := networkSessionTeardownCoordinator{
		store: store,
		cleanupDataPlane: func(context.Context) (api.LifecycleResponse, error) {
			return api.LifecycleResponse{Connection: "inactive"}, nil
		},
		removeProtection:       func(context.Context) error { return errors.New("synthetic envelope removal failure") },
		verifyRemainingNetwork: func(context.Context) error { verifyCalls++; return nil },
	}

	_, err := coordinator.Teardown(context.Background(), networkSessionTeardownTerminal)
	if err == nil {
		t.Fatal("expected envelope removal failure")
	}
	state, exists, loadErr := store.Load()
	if loadErr != nil || !exists || state.Protection == nil {
		t.Fatalf("envelope failure lost exact recovery authority: exists=%v state=%#v err=%v", exists, state, loadErr)
	}
	if state.Intent != networkSessionIntentTerminal {
		t.Fatalf("terminal decision was not durable: %q", state.Intent)
	}
	if verifyCalls != 0 {
		t.Fatalf("post-network verification ran before protection removal converged: %d", verifyCalls)
	}
}

func TestNetworkSessionTeardownPostNetworkFailureNeverRearmsResume(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentResume)
	coordinator := networkSessionTeardownCoordinator{
		store: store,
		cleanupDataPlane: func(context.Context) (api.LifecycleResponse, error) {
			return api.LifecycleResponse{Connection: "inactive"}, nil
		},
		removeProtection: func(context.Context) error { return store.SetProtection(nil) },
		verifyRemainingNetwork: func(context.Context) error {
			return errors.New("synthetic post-network verification failure")
		},
	}

	_, err := coordinator.Teardown(context.Background(), networkSessionTeardownTerminal)
	if err == nil {
		t.Fatal("expected post-network verification failure")
	}
	state, exists, loadErr := store.Load()
	if loadErr != nil || !exists {
		t.Fatalf("post-network failure lost terminal session evidence: exists=%v err=%v", exists, loadErr)
	}
	if state.Intent != networkSessionIntentTerminal || state.Protection != nil {
		t.Fatalf("post-network failure must remain terminal with removed envelope recorded: %#v", state)
	}
}

func TestNetworkSessionCleanupStatusGuardRefusesFalseInactivePublication(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentDisconnect)
	status := api.StatusResponse{Connection: "inactive", Mode: planner.ModeTun}
	guarded := guardNetworkSessionCleanupStatus(store, status)
	if guarded.Connection == "inactive" {
		t.Fatalf("cleanup authority must prevent clean disconnected publication: %#v", guarded)
	}

	if err := store.SetProtection(nil); err != nil {
		t.Fatal(err)
	}
	guarded = guardNetworkSessionCleanupStatus(store, status)
	if guarded.Connection == "inactive" {
		t.Fatalf("terminal/disconnect intent must remain non-clean until session authority clears: %#v", guarded)
	}
	if err := store.Remove(); err != nil {
		t.Fatal(err)
	}
	guarded = guardNetworkSessionCleanupStatus(store, status)
	if guarded.Connection != "inactive" {
		t.Fatalf("cleared authority should permit clean inactive publication: %#v", guarded)
	}
}

func seededProtectedNetworkSessionStore(t *testing.T, intent networkSessionIntent) networkSessionStateStore {
	t.Helper()
	store := newNetworkSessionStateStore(t.TempDir(), fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	protection := networkSessionProtection{
		State:              networkSessionProtectionArmed,
		CompositionVersion: privacyEnvelopeCompositionVersion,
		Family:             privacyEnvelopeFamily,
		Table:              "podlaz_pe_001122334455",
		TunInterface:       "podlaz0",
		BootstrapIPv4:      []string{"192.0.2.10"},
	}
	if err := store.SetProtection(&protection); err != nil {
		t.Fatal(err)
	}
	if intent != networkSessionIntentResume {
		if err := store.SetIntent(intent); err != nil {
			t.Fatal(err)
		}
	}
	return store
}
