package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestTunRevalidationRuntimeClassifiesMaterialGenerationChange(t *testing.T) {
	baseline := tunUplinkFingerprint{Interface: "wlan0", InterfaceIndex: 3, Gateway: "192.0.2.1", Addresses: "192.0.2.55/24"}
	changed := baseline
	changed.Gateway = "192.0.2.254"
	inspectCalls := 0
	verificationStarted := make(chan struct{})
	releaseVerification := make(chan struct{})

	runtime := newTunRevalidationRuntime(
		func(context.Context) (tunRevalidationObservation, error) {
			fingerprint := baseline
			if inspectCalls > 0 {
				fingerprint = changed
			}
			inspectCalls++
			return tunRevalidationObservation{fingerprint: fingerprint}, nil
		},
		func(context.Context, tunRevalidationObservation) error {
			close(verificationStarted)
			<-releaseVerification
			return nil
		},
	)
	runtime.Initialize(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.Revalidate(context.Background(), tunRevalidationTriggerRoute)
	}()
	select {
	case <-verificationStarted:
	case <-time.After(time.Second):
		t.Fatal("changed-generation verification did not start")
	}
	assertTunHealth(t, runtime.Health(), api.TunHealthRevalidating, 2, api.TunHealthUplinkChanged)
	close(releaseVerification)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("changed-generation verification did not complete")
	}
}
