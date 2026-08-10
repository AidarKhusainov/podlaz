package daemon

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type lifecycleOperationLock struct {
	token chan struct{}

	pendingMutations atomic.Int32

	cancelMu           sync.RWMutex
	cancelRevalidation context.CancelFunc
}

func newLifecycleOperationLock() *lifecycleOperationLock {
	lock := &lifecycleOperationLock{token: make(chan struct{}, 1)}
	lock.token <- struct{}{}
	return lock
}

func (l *lifecycleOperationLock) wrap(lifecycle lifecycleService) lifecycleService {
	if l == nil || lifecycle == nil {
		return lifecycle
	}
	return operationLockedLifecycle{lock: l, lifecycle: lifecycle}
}

func (l *lifecycleOperationLock) setRevalidationCancel(cancel context.CancelFunc) {
	if l == nil {
		return
	}
	l.cancelMu.Lock()
	l.cancelRevalidation = cancel
	l.cancelMu.Unlock()
}

func (l *lifecycleOperationLock) interruptRevalidation() {
	if l == nil {
		return
	}
	l.cancelMu.RLock()
	cancel := l.cancelRevalidation
	l.cancelMu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

// beginMutation declares mutation intent before cancelling any active probe.
// This closes the race where a newly queued revalidation could otherwise start
// after cancellation but before connect/disconnect/recovery acquired the shared
// lifecycle token. The returned function must be called after the mutation has
// released the token.
func (l *lifecycleOperationLock) beginMutation() func() {
	if l == nil {
		return func() {}
	}
	l.pendingMutations.Add(1)
	l.interruptRevalidation()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.pendingMutations.Add(-1)
		})
	}
}

func (l *lifecycleOperationLock) acquire(ctx context.Context) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.token:
		return nil
	}
}

func (l *lifecycleOperationLock) release() {
	if l == nil {
		return
	}
	l.token <- struct{}{}
}

func (l *lifecycleOperationLock) runRevalidation(ctx context.Context, fn func()) error {
	if l == nil {
		fn()
		return nil
	}
	if l.pendingMutations.Load() > 0 {
		return nil
	}
	if err := l.acquire(ctx); err != nil {
		return err
	}
	defer l.release()
	// A mutation may have declared intent while this revalidation was waiting
	// for the token. Yield the token without starting new probes in that case.
	if l.pendingMutations.Load() > 0 {
		return nil
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	fn()
	return nil
}

func (l *lifecycleOperationLock) runRecovery(ctx context.Context, fn func() api.RecoveryResponse) api.RecoveryResponse {
	if l == nil {
		return fn()
	}
	finishMutation := l.beginMutation()
	defer finishMutation()
	if err := l.acquire(ctx); err != nil {
		return api.RecoveryResponse{
			Mode: "execute",
			Warnings: []api.RecoveryWarning{{
				Target:  "lifecycle operation",
				Message: err.Error(),
			}},
		}
	}
	defer l.release()
	return fn()
}

type operationLockedLifecycle struct {
	lock      *lifecycleOperationLock
	lifecycle lifecycleService
}

func (l operationLockedLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	finishMutation := l.lock.beginMutation()
	defer finishMutation()
	if err := l.lock.acquire(ctx); err != nil {
		return api.LifecycleResponse{}, err
	}
	defer l.lock.release()
	return l.lifecycle.Connect(ctx, request)
}

func (l operationLockedLifecycle) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	finishMutation := l.lock.beginMutation()
	defer finishMutation()
	if err := l.lock.acquire(ctx); err != nil {
		return api.LifecycleResponse{}, err
	}
	defer l.lock.release()
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
