package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestIssue254DuplicateOrdinaryHintAdvancesPublicationWithoutExtraProof(t *testing.T) {
	fingerprint := tunUplinkFingerprint{Interface: "wlan0", InterfaceIndex: 3, Gateway: "192.0.2.1", Addresses: "192.0.2.55/24"}
	secondInspectStarted := make(chan struct{})
	releaseSecondInspect := make(chan struct{})
	completed := make(chan tunRevalidationTrigger, 2)
	var inspectCalls atomic.Int32
	var verifyCalls atomic.Int32

	runtime := newTunRevalidationRuntime(
		func(context.Context) (tunRevalidationObservation, error) {
			if inspectCalls.Add(1) == 2 {
				close(secondInspectStarted)
				<-releaseSecondInspect
			}
			return tunRevalidationObservation{fingerprint: fingerprint}, nil
		},
		func(context.Context, tunRevalidationObservation) error {
			verifyCalls.Add(1)
			return nil
		},
	)
	runtime.PrepareInitialize()
	coordinator := newTunRevalidationOutcomeCoordinator(func(ctx context.Context, trigger tunRevalidationTrigger) tunRevalidationOutcome {
		var outcome tunRevalidationOutcome
		if trigger == tunRevalidationTriggerInitial {
			outcome = runtime.InitializePending(ctx)
		} else {
			outcome = runtime.Revalidate(ctx, trigger)
		}
		completed <- trigger
		return outcome
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go coordinator.Run(ctx)

	coordinator.Notify(tunRevalidationTriggerInitial)
	select {
	case trigger := <-completed:
		if trigger != tunRevalidationTriggerInitial {
			t.Fatalf("first trigger=%q, want initial", trigger)
		}
	case <-time.After(time.Second):
		t.Fatal("initial revalidation did not complete")
	}
	assertTunHealth(t, runtime.Health(), api.TunHealthVerified, 1, "")

	coordinator.Notify(tunRevalidationTriggerRoute)
	select {
	case <-secondInspectStarted:
	case <-time.After(time.Second):
		t.Fatal("duplicate route observation did not start")
	}
	assertTunHealth(t, runtime.Health(), api.TunHealthRevalidating, 1, api.TunHealthUplinkRevalidating)
	close(releaseSecondInspect)
	select {
	case trigger := <-completed:
		if trigger != tunRevalidationTriggerRoute {
			t.Fatalf("second trigger=%q, want route", trigger)
		}
	case <-time.After(time.Second):
		t.Fatal("duplicate route observation did not complete")
	}
	assertTunHealth(t, runtime.Health(), api.TunHealthVerified, 1, "")
	if got := verifyCalls.Load(); got != 1 {
		t.Fatalf("verification calls=%d, want only initial proof", got)
	}
}
