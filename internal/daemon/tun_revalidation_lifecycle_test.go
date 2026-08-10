package daemon

import (
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
