package daemon

import (
	"context"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type startupScanRefreshingLifecycle struct {
	lifecycle *XrayManager
	refresh   func(context.Context)
}

func (l startupScanRefreshingLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	defer l.refreshAfter(ctx)
	return l.lifecycle.Connect(ctx, request)
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
