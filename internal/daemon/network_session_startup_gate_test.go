package daemon

import (
	"context"
	"errors"
	"testing"
)

func TestNetworkSessionStartupGateBlocksLifecycleMutationsUntilReleased(t *testing.T) {
	events := []string{}
	inner := networkSessionRecordingLifecycle{events: &events}
	gate := newNetworkSessionStartupMutationGate(inner)
	gate.Block()

	if _, err := gate.Connect(context.Background(), testContinuationRequest()); !errors.Is(err, errNetworkSessionStartupRecoveryPending) {
		t.Fatalf("blocked connect error = %v", err)
	}
	if _, err := gate.Disconnect(context.Background()); !errors.Is(err, errNetworkSessionStartupRecoveryPending) {
		t.Fatalf("blocked disconnect error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("blocked lifecycle must not reach inner service: %#v", events)
	}

	gate.Release()
	if _, err := gate.Connect(context.Background(), testContinuationRequest()); err != nil {
		t.Fatalf("connect after release: %v", err)
	}
	if _, err := gate.Disconnect(context.Background()); err != nil {
		t.Fatalf("disconnect after release: %v", err)
	}
	if len(events) != 2 || events[0] != "connect" || events[1] != "disconnect" {
		t.Fatalf("released lifecycle events = %#v", events)
	}
}

func TestNetworkSessionStartupGateStartsReleased(t *testing.T) {
	events := []string{}
	gate := newNetworkSessionStartupMutationGate(networkSessionRecordingLifecycle{events: &events})

	if _, err := gate.Connect(context.Background(), testContinuationRequest()); err != nil {
		t.Fatalf("startup gate unexpectedly blocked: %v", err)
	}
	if len(events) != 1 || events[0] != "connect" {
		t.Fatalf("released startup gate did not delegate connect: %#v", events)
	}
}
