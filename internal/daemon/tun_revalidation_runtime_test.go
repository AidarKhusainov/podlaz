package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestTunRevalidationRuntimeAdvancesOnlyForChangedFingerprint(t *testing.T) {
	first := tunUplinkFingerprint{Interface: "wlan0", InterfaceIndex: 3, Gateway: "192.0.2.1", Addresses: "192.0.2.55/24"}
	second := first
	second.Gateway = "192.0.2.254"
	observations := []tunRevalidationObservation{{fingerprint: first}, {fingerprint: first}, {fingerprint: second}}
	inspectCalls := 0
	verifyCalls := 0
	runtime := newTunRevalidationRuntime(
		func(context.Context) (tunRevalidationObservation, error) {
			observation := observations[inspectCalls]
			inspectCalls++
			return observation, nil
		},
		func(context.Context, tunRevalidationObservation) error {
			verifyCalls++
			return nil
		},
	)

	runtime.Initialize(context.Background())
	assertTunHealth(t, runtime.Health(), api.TunHealthVerified, 1, "")
	runtime.Revalidate(context.Background(), tunRevalidationTriggerRoute)
	assertTunHealth(t, runtime.Health(), api.TunHealthVerified, 1, "")
	if verifyCalls != 0 {
		t.Fatalf("unchanged fingerprint ran %d verifications, want 0", verifyCalls)
	}
	runtime.Revalidate(context.Background(), tunRevalidationTriggerAddress)
	assertTunHealth(t, runtime.Health(), api.TunHealthVerified, 2, "")
	if verifyCalls != 1 {
		t.Fatalf("changed fingerprint ran %d verifications, want 1", verifyCalls)
	}
}

func TestTunRevalidationRuntimeInvalidatesHealthyStateWhenFingerprintUnavailable(t *testing.T) {
	fingerprint := tunUplinkFingerprint{Interface: "wlan0", InterfaceIndex: 3, Gateway: "192.0.2.1", Addresses: "192.0.2.55/24"}
	calls := 0
	runtime := newTunRevalidationRuntime(
		func(context.Context) (tunRevalidationObservation, error) {
			calls++
			if calls == 1 {
				return tunRevalidationObservation{fingerprint: fingerprint}, nil
			}
			return tunRevalidationObservation{}, newTunRevalidationObservationError(api.TunHealthUplinkFingerprintUnavailable, errors.New("default route unavailable"))
		},
		func(context.Context, tunRevalidationObservation) error { return nil },
	)

	runtime.Initialize(context.Background())
	runtime.Revalidate(context.Background(), tunRevalidationTriggerResume)
	assertTunHealth(t, runtime.Health(), api.TunHealthDegraded, 1, api.TunHealthUplinkFingerprintUnavailable)
}

func TestTunRevalidationRuntimeFailsClosedOnOwnershipMismatch(t *testing.T) {
	fingerprints := []tunUplinkFingerprint{
		{Interface: "wlan0", InterfaceIndex: 3, Gateway: "192.0.2.1", Addresses: "192.0.2.55/24"},
		{Interface: "wlan0", InterfaceIndex: 3, Gateway: "192.0.2.254", Addresses: "192.0.2.55/24"},
	}
	calls := 0
	runtime := newTunRevalidationRuntime(
		func(context.Context) (tunRevalidationObservation, error) {
			observation := tunRevalidationObservation{fingerprint: fingerprints[calls]}
			calls++
			return observation, nil
		},
		func(context.Context, tunRevalidationObservation) error {
			return newTunRevalidationVerificationError(api.TunHealthOwnershipInvalid, errors.New("TUN ifindex mismatch"))
		},
	)

	runtime.Initialize(context.Background())
	runtime.Revalidate(context.Background(), tunRevalidationTriggerLink)
	assertTunHealth(t, runtime.Health(), api.TunHealthCleanupRequired, 2, api.TunHealthOwnershipInvalid)
}

func TestTunRevalidationRuntimeMapsCancellationAndTimeout(t *testing.T) {
	baseline := tunUplinkFingerprint{Interface: "wlan0", InterfaceIndex: 3, Gateway: "192.0.2.1", Addresses: "192.0.2.55/24"}
	changed := baseline
	changed.Gateway = "192.0.2.254"

	for _, tc := range []struct {
		name           string
		verificationErr error
		want            api.TunHealthClassification
	}{
		{name: "cancelled", verificationErr: context.Canceled, want: api.TunHealthRevalidationInterrupted},
		{name: "timeout", verificationErr: context.DeadlineExceeded, want: api.TunHealthRevalidationTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			runtime := newTunRevalidationRuntime(
				func(context.Context) (tunRevalidationObservation, error) {
					fingerprint := baseline
					if calls > 0 {
						fingerprint = changed
					}
					calls++
					return tunRevalidationObservation{fingerprint: fingerprint}, nil
				},
				func(context.Context, tunRevalidationObservation) error { return tc.verificationErr },
			)
			runtime.Initialize(context.Background())
			runtime.Revalidate(context.Background(), tunRevalidationTriggerResume)
			assertTunHealth(t, runtime.Health(), api.TunHealthDegraded, 2, tc.want)
		})
	}
}

func assertTunHealth(t *testing.T, health *api.TunHealthStatus, state api.TunHealthState, generation uint64, classification api.TunHealthClassification) {
	t.Helper()
	if health == nil {
		t.Fatal("TUN health is nil")
	}
	if health.State != state || health.NetworkGeneration != generation || health.Classification != classification {
		t.Fatalf("TUN health = %#v; want state=%q generation=%d classification=%q", health, state, generation, classification)
	}
}
