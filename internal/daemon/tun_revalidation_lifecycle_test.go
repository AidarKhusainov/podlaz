package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestDecorateTunHealthFailsClosedWhenActiveTunHealthIsNotInitialized(t *testing.T) {
	status := decorateTunHealth(api.StatusResponse{
		Connection: "active",
		Mode:       planner.ModeTun,
	}, newTunRevalidationRuntime(nil, nil))

	if status.TunHealth == nil {
		t.Fatal("active TUN status omitted current health while initialization was incomplete")
	}
	if status.TunHealth.State != api.TunHealthRevalidating || status.TunHealth.NetworkGeneration != 1 || status.TunHealth.Classification != api.TunHealthUplinkRevalidating {
		t.Fatalf("active TUN health=%#v, want fail-closed generation-1 revalidating state", status.TunHealth)
	}
}

func TestDecorateTunHealthOmitsHealthOutsideActiveTunMode(t *testing.T) {
	runtime := newTunRevalidationRuntime(nil, nil)
	for _, status := range []api.StatusResponse{
		{Connection: "inactive", Mode: planner.ModeTun},
		{Connection: "active", Mode: planner.ModeProxyOnly},
	} {
		got := decorateTunHealth(status, runtime)
		if got.TunHealth != nil {
			t.Fatalf("status %#v unexpectedly published TUN health %#v", status, got.TunHealth)
		}
	}
}

func TestTunRevalidationLifecyclePublishesRevalidatingDuringTunReplaceAndRestoresOnFailure(t *testing.T) {
	fingerprint := tunUplinkFingerprint{Interface: "wlan0", InterfaceIndex: 3, Gateway: "192.0.2.1", Addresses: "192.0.2.55/24"}
	runtime := newTunRevalidationRuntime(
		func(context.Context) (tunRevalidationObservation, error) {
			return tunRevalidationObservation{fingerprint: fingerprint}, nil
		},
		func(context.Context, tunRevalidationObservation) error { return nil },
	)
	runtime.Initialize(context.Background())

	connectErr := errors.New("replacement preflight failed")
	fake := &issue245TransitionLifecycle{runtime: runtime, connectErr: connectErr}
	lifecycle := tunRevalidationLifecycle{lifecycle: fake, runtime: runtime}
	_, err := lifecycle.Connect(context.Background(), api.ConnectRequest{Mode: planner.ModeTun})
	if !errors.Is(err, connectErr) {
		t.Fatalf("connect error=%v, want replacement failure", err)
	}
	if fake.observed == nil || fake.observed.State != api.TunHealthRevalidating || fake.observed.NetworkGeneration != 1 {
		t.Fatalf("health observed during replace=%#v, want generation-1 revalidating", fake.observed)
	}
	assertTunHealth(t, runtime.Health(), api.TunHealthVerified, 1, "")
}

type issue245TransitionLifecycle struct {
	runtime    *tunRevalidationRuntime
	connectErr error
	observed   *api.TunHealthStatus
}

func (l *issue245TransitionLifecycle) Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
	l.observed = l.runtime.Health()
	return api.LifecycleResponse{}, l.connectErr
}

func (l *issue245TransitionLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{}, nil
}
