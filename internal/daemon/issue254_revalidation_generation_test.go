package daemon

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestIssue254NewerNetworkHintPreventsOlderTerminalPublication(t *testing.T) {
	baseline := tunUplinkFingerprint{Interface: "wlan0", InterfaceIndex: 3, Gateway: "192.0.2.1", Addresses: "192.0.2.55/24"}
	changed := baseline
	changed.Gateway = "192.0.2.254"

	verifyStarted := make(chan struct{})
	releaseOlder := make(chan struct{})
	freshFinished := make(chan struct{})
	var inspectCalls atomic.Int32
	var verifyCalls atomic.Int32
	runtime := newTunRevalidationRuntime(
		func(context.Context) (tunRevalidationObservation, error) {
			call := inspectCalls.Add(1)
			fingerprint := baseline
			if call > 1 {
				fingerprint = changed
			}
			return tunRevalidationObservation{fingerprint: fingerprint}, nil
		},
		func(context.Context, tunRevalidationObservation) error {
			switch verifyCalls.Add(1) {
			case 1:
				return nil
			case 2:
				close(verifyStarted)
				<-releaseOlder
				return newTunRevalidationVerificationError(api.TunHealthOwnedStateInvalid, errors.New("older observation failed"))
			default:
				close(freshFinished)
				return nil
			}
		},
	)
	if err := runtime.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	assertTunHealth(t, runtime.Health(), api.TunHealthVerified, 1, "")

	var terminalCalls atomic.Int32
	coordinator := newTunRevalidationOutcomeCoordinator(
		func(ctx context.Context, trigger tunRevalidationTrigger) tunRevalidationOutcome {
			return runtime.Revalidate(ctx, trigger)
		},
		func(context.Context, tunRevalidationOutcome) {
			terminalCalls.Add(1)
		},
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinator.Run(ctx)

	coordinator.Notify(tunRevalidationTriggerRoute)
	select {
	case <-verifyStarted:
	case <-time.After(time.Second):
		t.Fatal("older revalidation did not reach verification")
	}
	assertTunHealth(t, runtime.Health(), api.TunHealthRevalidating, 2, api.TunHealthUplinkChanged)

	// Resume is newer evidence while generation 2 is still being verified.
	// The older failure may return, but it must neither publish terminal health
	// nor cross the terminal-cleanup handoff.
	coordinator.Notify(tunRevalidationTriggerResume)
	close(releaseOlder)

	select {
	case <-freshFinished:
	case <-time.After(time.Second):
		t.Fatal("newer revalidation did not complete")
	}
	assertTunHealth(t, runtime.Health(), api.TunHealthVerified, 2, "")
	if got := terminalCalls.Load(); got != 0 {
		t.Fatalf("stale older outcome reached terminal cleanup %d time(s), want 0", got)
	}
}

func TestIssue254HealthHidesResultFromSupersededPublicationRevision(t *testing.T) {
	currentRevision := uint64(2)
	runtime := newTunRevalidationRuntime(nil, nil)
	runtime.health = &api.TunHealthStatus{State: api.TunHealthVerified, NetworkGeneration: 7}
	runtime.healthPublication = tunRevalidationPublicationToken{
		revision: 1,
		current:  func() uint64 { return currentRevision },
	}
	runtime.hasHealthPublication = true

	assertTunHealth(t, runtime.Health(), api.TunHealthRevalidating, 7, api.TunHealthUplinkRevalidating)
}
