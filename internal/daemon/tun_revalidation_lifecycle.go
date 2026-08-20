package daemon

import (
	"context"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type tunRevalidationLifecycle struct {
	lifecycle   lifecycleService
	runtime     *tunRevalidationRuntime
	schedule    func(tunRevalidationTrigger)
	cancelRetry func()
}

func (l tunRevalidationLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	if l.cancelRetry != nil {
		l.cancelRetry()
	}
	var previousHealth *api.TunHealthStatus
	if l.runtime != nil && request.Mode == planner.ModeTun {
		previousHealth = l.runtime.beginLifecycleTransition()
	}

	response, err := l.lifecycle.Connect(ctx, request)
	if err != nil {
		// A failed replace-podlaz attempt may leave the previously active TUN
		// session untouched. Restore the exact previous current-health evidence;
		// beginLifecycleTransition intentionally did not modify its fingerprint.
		if l.runtime != nil && request.Mode == planner.ModeTun {
			l.runtime.restoreHealth(previousHealth)
		}
		return response, err
	}
	if l.runtime == nil {
		return response, nil
	}
	if request.Mode != planner.ModeTun {
		l.runtime.Clear()
		return response, nil
	}

	// Publish generation one as unverified while lifecycle mutation authority is
	// still held, but do not run network probes here. The coordinator consumes
	// the initial trigger only after the operation lock is released, so
	// disconnect/recovery/shutdown can cancel the proof through the same external
	// control path as every later revalidation.
	l.runtime.PrepareInitialize()
	if l.schedule != nil {
		l.schedule(tunRevalidationTriggerInitial)
	}
	return response, nil
}

func (l tunRevalidationLifecycle) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	if l.cancelRetry != nil {
		l.cancelRetry()
	}
	response, err := l.lifecycle.Disconnect(ctx)
	if err == nil && l.runtime != nil {
		l.runtime.Clear()
	}
	return response, err
}

func decorateTunHealth(status api.StatusResponse, runtime *tunRevalidationRuntime) api.StatusResponse {
	if runtime == nil || status.Connection != "active" || status.Mode != planner.ModeTun {
		status.TunHealth = nil
		return status
	}
	health := runtime.Health()
	if health == nil {
		// XrayManager can publish active immediately after commit while baseline
		// fingerprint initialization is still in progress. Missing evidence must
		// never be interpreted as implicit healthy current state.
		health = &api.TunHealthStatus{
			State:             api.TunHealthRevalidating,
			NetworkGeneration: 1,
			Classification:    api.TunHealthUplinkRevalidating,
		}
	}
	status.TunHealth = health
	return status
}
