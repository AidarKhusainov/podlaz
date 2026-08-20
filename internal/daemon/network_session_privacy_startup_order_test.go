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

	resumed, err := resumeNetworkSessionWithRecoveryStages(
		context.Background(),
		continuation,
		lifecycle,
		func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
		func(context.Context, api.StatusResponse) api.RecoveryResponse {
			events = append(events, "generic-recovery")
			return api.RecoveryResponse{Mode: "execute"}
		},
		func(context.Context, networkSessionStateStore) error {
			events = append(events, "privacy-reconcile")
			return nil
		},
		func(context.Context, string) api.RecoveryResponse {
			events = append(events, "exact-data-plane-recovery")
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

func TestResumeNetworkSessionStopsBeforeDataPlaneRecoveryWhenPrivacyReconcileFails(t *testing.T) {
	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatalf("save continuation: %v", err)
	}
	events := []string{}
	lifecycle := newNetworkSessionLifecycle(networkSessionRecordingLifecycle{events: &events}, continuation)

	resumed, err := resumeNetworkSessionWithRecoveryStages(
		context.Background(),
		continuation,
		lifecycle,
		func(context.Context) api.StatusResponse { return api.StatusResponse{Connection: "inactive"} },
		func(context.Context, api.StatusResponse) api.RecoveryResponse {
			events = append(events, "generic-recovery")
			return api.RecoveryResponse{Mode: "execute"}
		},
		func(context.Context, networkSessionStateStore) error {
			events = append(events, "privacy-reconcile")
			return errNetworkSessionRecoveryIncomplete
		},
		func(context.Context, string) api.RecoveryResponse {
			events = append(events, "exact-data-plane-recovery")
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
