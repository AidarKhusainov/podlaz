package daemon

import (
	"context"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func (l *lifecycleOperationLock) disconnectForRestart(ctx context.Context, lifecycle restartDisconnectLifecycle) (api.LifecycleResponse, error) {
	if l == nil {
		return lifecycle.DisconnectForRestart(ctx)
	}
	finishMutation, err := l.beginExternalMutation()
	if err != nil {
		return api.LifecycleResponse{}, err
	}
	defer finishMutation()
	if err := l.acquire(ctx); err != nil {
		return api.LifecycleResponse{}, err
	}
	defer l.release()
	return lifecycle.DisconnectForRestart(ctx)
}
