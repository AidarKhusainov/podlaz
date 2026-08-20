package daemon

import (
	"context"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestResumeNetworkSessionReconcilesProtectionBeforeDataPlaneRecovery(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatalf("save continuation: %v", err)
	}
	events := []string{}
	lifecycle := newNetworkSessionLifecycle(networkSessionRecordingLifecycle{events: &events}, continuation)
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error {
		events = append(events, "privacy-reconcile")
		return nil
	}
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
		events = append(events, "exact-data-plane-recovery")
		return api.RecoveryResponse{Mode: "execute"}
	}

	resumed, err := resumeNetworkSession(
		context.Background(),
		continuation,
		lifecycle,
		func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
		func(context.Context, api.StatusResponse) api.RecoveryResponse {
			events = append(events, "generic-recovery")
			return api.RecoveryResponse{Mode: "execute"}
		},
	)
	if err != nil {
		t.Fatalf("resume network session: %v", err)
	}
	if !resumed {
		t.Fatal("expected protected session resume")
	}
	want := []string{"privacy-reconcile", "exact-data-plane-recovery", "generic-recovery", "connect"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("startup ordering = %#v, want %#v", events, want)
	}
}

func TestResumeNetworkSessionReloadsRequestAfterReplacementReconciliation(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	store := continuation.stateStore()
	previous := testContinuationRequest()
	if _, err := store.BeginOrResume(previous); err != nil {
		t.Fatal(err)
	}
	protection := testArmedPrivacyProtection()
	if err := store.SetProtection(&protection); err != nil {
		t.Fatal(err)
	}
	target := previous
	target.Handoff = api.HandoffReplacePodlaz
	target.Profile.ID = "profile-replacement"
	target.Profile.Name = "Replacement profile"
	target.Profile.Server = "replacement.example.test"
	if _, err := store.BeginOrResume(target); err != nil {
		t.Fatal(err)
	}

	continuation.reconcilePrivacy = func(_ context.Context, stateStore networkSessionStateStore) error {
		return stateStore.RestoreReplacement()
	}
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
		return api.RecoveryResponse{Mode: "execute"}
	}
	capture := &networkSessionRequestCaptureLifecycle{}

	resumed, err := resumeNetworkSession(
		context.Background(),
		continuation,
		capture,
		func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
		func(context.Context, api.StatusResponse) api.RecoveryResponse { return api.RecoveryResponse{Mode: "execute"} },
	)
	if err != nil || !resumed {
		t.Fatalf("resume restored session: resumed=%v err=%v", resumed, err)
	}
	if len(capture.requests) != 1 {
		t.Fatalf("connect requests=%d, want 1", len(capture.requests))
	}
	if !reflect.DeepEqual(capture.requests[0], previous) {
		t.Fatalf("startup replayed stale replacement request: got=%#v want=%#v", capture.requests[0], previous)
	}
}

func TestResumeNetworkSessionStopsBeforeDataPlaneRecoveryWhenPrivacyReconcileFails(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatalf("save continuation: %v", err)
	}
	events := []string{}
	lifecycle := newNetworkSessionLifecycle(networkSessionRecordingLifecycle{events: &events}, continuation)
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error {
		events = append(events, "privacy-reconcile")
		return errNetworkSessionRecoveryIncomplete
	}
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
		events = append(events, "exact-data-plane-recovery")
		return api.RecoveryResponse{Mode: "execute"}
	}

	resumed, err := resumeNetworkSession(
		context.Background(),
		continuation,
		lifecycle,
		func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
		func(context.Context, api.StatusResponse) api.RecoveryResponse {
			events = append(events, "generic-recovery")
			return api.RecoveryResponse{Mode: "execute"}
		},
	)
	if err == nil || resumed {
		t.Fatalf("privacy reconciliation failure must block resume: resumed=%v err=%v", resumed, err)
	}
	if !reflect.DeepEqual(events, []string{"privacy-reconcile"}) {
		t.Fatalf("privacy failure must precede all data-plane mutation: %#v", events)
	}
}

func TestResumeNetworkSessionTerminalConvergenceRunsExactAndGenericCleanupBeforeTeardown(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatalf("save continuation: %v", err)
	}
	if err := continuation.stateStore().SetIntent(networkSessionIntentTerminal); err != nil {
		t.Fatalf("persist terminal intent: %v", err)
	}

	events := []string{}
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error {
		events = append(events, "unexpected-resume-reconcile")
		return nil
	}
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
		events = append(events, "exact-data-plane-recovery")
		return api.RecoveryResponse{Mode: "execute"}
	}
	continuation.continueTeardown = func(_ context.Context, store networkSessionStateStore) error {
		events = append(events, "terminal-teardown")
		return store.Remove()
	}

	resumed, err := resumeNetworkSession(
		context.Background(),
		continuation,
		networkSessionRecordingLifecycle{events: &events},
		func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
		func(context.Context, api.StatusResponse) api.RecoveryResponse {
			events = append(events, "generic-recovery")
			return api.RecoveryResponse{Mode: "execute"}
		},
	)
	if err != nil || resumed {
		t.Fatalf("terminal convergence: resumed=%v err=%v", resumed, err)
	}
	want := []string{"exact-data-plane-recovery", "generic-recovery", "terminal-teardown"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("terminal startup ordering = %#v, want %#v", events, want)
	}
}

type networkSessionRequestCaptureLifecycle struct {
	requests []api.ConnectRequest
}

func (l *networkSessionRequestCaptureLifecycle) Connect(_ context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	l.requests = append(l.requests, request)
	return api.LifecycleResponse{Connection: "active", Mode: request.Mode, Proxy: "inactive", TUN: "active"}, nil
}

func (l *networkSessionRequestCaptureLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
}
