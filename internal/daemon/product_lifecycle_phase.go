package daemon

import (
	"context"
	"sync"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type productLifecyclePhaseTracker struct {
	mu      sync.RWMutex
	request *api.ConnectRequest
}

func (t *productLifecyclePhaseTracker) beginConnect(request api.ConnectRequest) {
	if t == nil {
		return
	}
	copyRequest := request
	t.mu.Lock()
	t.request = &copyRequest
	t.mu.Unlock()
}

func (t *productLifecyclePhaseTracker) endConnect() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.request = nil
	t.mu.Unlock()
}

func (t *productLifecyclePhaseTracker) decorate(status api.StatusResponse) api.StatusResponse {
	if t == nil {
		return status
	}
	t.mu.RLock()
	request := t.request
	if request != nil {
		copyRequest := *request
		request = &copyRequest
	}
	t.mu.RUnlock()
	if request == nil {
		return status
	}
	status.LifecyclePhase = api.LifecycleConnecting
	status.Mode = request.Mode
	status.ProfileID = request.Profile.ID
	status.ProfileName = request.Profile.Name
	return status
}

type productPhaseLifecycle struct {
	inner   lifecycleService
	tracker *productLifecyclePhaseTracker
}

func (l productPhaseLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	l.tracker.beginConnect(request)
	defer l.tracker.endConnect()
	return l.inner.Connect(ctx, request)
}

func (l productPhaseLifecycle) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	return l.inner.Disconnect(ctx)
}
