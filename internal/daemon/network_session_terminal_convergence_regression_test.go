package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestTerminalNetworkSessionConvergenceHasOneCleanupOwner(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	if err := continuation.stateStore().SetIntent(networkSessionIntentTerminal); err != nil {
		t.Fatal(err)
	}

	events := []string{}
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
		events = append(events, "exact-data-plane-recovery")
		return api.RecoveryResponse{Mode: "execute"}
	}
	continuation.continueTeardown = func(_ context.Context, store networkSessionStateStore) error {
		events = append(events, "terminal-teardown")
		return store.Remove()
	}
	genericCalled := false

	resumed, err := resumeNetworkSession(
		context.Background(),
		continuation,
		networkSessionRecordingLifecycle{events: &events},
		func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "active"} },
		func(context.Context, api.StatusResponse) api.RecoveryResponse {
			genericCalled = true
			return api.RecoveryResponse{
				Mode: "execute",
				Warnings: []api.RecoveryWarning{{
					Target:  "systemd-resolved link podlaz0",
					Message: "synthetic bounded inspection timeout",
				}},
			}
		},
	)
	if err != nil || resumed {
		t.Fatalf("terminal convergence: resumed=%v err=%v", resumed, err)
	}
	if genericCalled {
		t.Fatal("generic recovery must not duplicate or veto exact terminal convergence")
	}
	want := []string{"exact-data-plane-recovery", "terminal-teardown"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("terminal ordering=%#v, want %#v", events, want)
	}
}

func TestTerminalNetworkSessionKeepsProtectionWhenExactRecoveryIsIncomplete(t *testing.T) {
	store := seededProtectedNetworkSessionStore(t, networkSessionIntentDisconnect)
	continuation := newNetworkSessionContinuationStore(store.runtimeDir, fixedBootID("boot-a"))
	blocker := errors.New("synthetic exact transaction DNS cleanup blocker")
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
		return api.RecoveryResponse{
			Mode: "execute",
			Results: []api.RecoveryCleanupResult{{
				Candidate: api.RecoveryCandidate{Kind: "dns-link", Target: "podlaz0"},
				Status:    "failed",
				Message:   blocker.Error(),
			}},
		}
	}
	teardownCalled := false
	continuation.continueTeardown = func(context.Context, networkSessionStateStore) error {
		teardownCalled = true
		return nil
	}

	resumed, err := resumeNetworkSession(
		context.Background(),
		continuation,
		networkSessionRecordingLifecycle{events: &[]string{}},
		func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "active"} },
		func(context.Context, api.StatusResponse) api.RecoveryResponse {
			return api.RecoveryResponse{Mode: "execute"}
		},
	)
	if err == nil || resumed {
		t.Fatalf("incomplete exact recovery must stop terminal teardown: resumed=%v err=%v", resumed, err)
	}
	if teardownCalled {
		t.Fatal("Privacy Envelope teardown ran before exact data-plane cleanup converged")
	}
	assertProtectionState(t, store, networkSessionProtectionArmed)
}
