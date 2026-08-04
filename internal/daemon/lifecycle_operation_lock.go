package daemon

import (
	"context"
	"sync"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type lifecycleOperationLock struct {
	mu sync.Mutex
}

func newLifecycleOperationLock() *lifecycleOperationLock {
	return &lifecycleOperationLock{}
}

func (l *lifecycleOperationLock) wrap(lifecycle lifecycleService) lifecycleService {
	if l == nil || lifecycle == nil {
		return lifecycle
	}
	return operationLockedLifecycle{lock: l, lifecycle: lifecycle}
}

func (l *lifecycleOperationLock) runRecovery(fn func() api.RecoveryResponse) api.RecoveryResponse {
	if l == nil {
		return fn()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return fn()
}

type operationLockedLifecycle struct {
	lock      *lifecycleOperationLock
	lifecycle lifecycleService
}

func (l operationLockedLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	l.lock.mu.Lock()
	defer l.lock.mu.Unlock()
	return l.lifecycle.Connect(ctx, request)
}

func (l operationLockedLifecycle) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	l.lock.mu.Lock()
	defer l.lock.mu.Unlock()
	return l.lifecycle.Disconnect(ctx)
}

func (l operationLockedLifecycle) Status(ctx context.Context) api.StatusResponse {
	reporter, ok := l.lifecycle.(interface {
		Status(context.Context) api.StatusResponse
	})
	if !ok {
		return api.StatusResponse{}
	}
	return reporter.Status(ctx)
}
