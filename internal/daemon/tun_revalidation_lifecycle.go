package daemon

import (
	"context"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const tunRevalidationInitializeTimeout = 15 * time.Second

type tunRevalidationLifecycle struct {
	lifecycle lifecycleService
	runtime   *tunRevalidationRuntime
}

func (l tunRevalidationLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	response, err := l.lifecycle.Connect(ctx, request)
	if err != nil {
		// A failed replace-podlaz attempt may leave the previously active TUN
		// session untouched. Preserve its current-health evidence until the
		// underlying lifecycle actually transitions away from that session.
		return response, err
	}
	if l.runtime == nil {
		return response, nil
	}
	if request.Mode != planner.ModeTun {
		l.runtime.Clear()
		return response, nil
	}

	// Once a committed session exists, capture its baseline even if the client
	// request context is cancelled immediately after commit. The bounded context
	// prevents this publication step from delaying a lifecycle operation forever.
	initializeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tunRevalidationInitializeTimeout)
	l.runtime.Initialize(initializeCtx)
	cancel()
	return response, nil
}

func (l tunRevalidationLifecycle) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
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
