package daemon

import (
	"context"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const unexpectedCoreExitRefreshTimeout = 5 * time.Second

type startupScanRefreshingLifecycle struct {
	lifecycle *XrayManager
	refresh   func(context.Context)
}

func (l startupScanRefreshingLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	response, err := l.lifecycle.Connect(ctx, request)
	if err == nil {
		l.watchUnexpectedCoreExit()
	}
	l.refreshAfter(ctx)
	return response, err
}

func (l startupScanRefreshingLifecycle) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	defer l.refreshAfter(ctx)
	return l.lifecycle.Disconnect(ctx)
}

func (l startupScanRefreshingLifecycle) Status(ctx context.Context) api.StatusResponse {
	return l.lifecycle.Status(ctx)
}

func (l startupScanRefreshingLifecycle) refreshAfter(ctx context.Context) {
	if l.refresh != nil {
		l.refresh(context.WithoutCancel(ctx))
	}
}

func (l startupScanRefreshingLifecycle) watchUnexpectedCoreExit() {
	if l.lifecycle == nil || l.refresh == nil {
		return
	}
	l.lifecycle.mu.Lock()
	done := l.lifecycle.done
	state := l.lifecycle.state
	l.lifecycle.mu.Unlock()

	if state.Connection == "error (core exited)" {
		if state.Mode == planner.ModeTun {
			go l.refreshAfterUnexpectedCoreExit()
		}
		return
	}
	if done == nil {
		return
	}
	go func() {
		<-done
		l.lifecycle.mu.Lock()
		state := l.lifecycle.state
		l.lifecycle.mu.Unlock()
		if state.Connection == "error (core exited)" && state.Mode == planner.ModeTun {
			l.refreshAfterUnexpectedCoreExit()
		}
	}()
}

func (l startupScanRefreshingLifecycle) refreshAfterUnexpectedCoreExit() {
	ctx, cancel := context.WithTimeout(context.Background(), unexpectedCoreExitRefreshTimeout)
	defer cancel()
	l.refresh(ctx)
}
