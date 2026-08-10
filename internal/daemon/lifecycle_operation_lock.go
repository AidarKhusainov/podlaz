package daemon

import (
	"context"
	"sync"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type lifecycleOperationLock struct {
	token chan struct{}

	mutationMu       sync.Mutex
	pendingMutations int
	mutationIdle     chan struct{}

	cancelMu           sync.RWMutex
	cancelRevalidation context.CancelFunc
}

func newLifecycleOperationLock() *lifecycleOperationLock {
	idle := make(chan struct{})
	close(idle)
	lock := &lifecycleOperationLock{
		token:        make(chan struct{}, 1),
		mutationIdle: idle,
	}
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
// New revalidation work waits for the whole mutation queue to become idle, so
// a network event consumed while connect/disconnect/recovery is running cannot
// disappear merely because lifecycle mutation has precedence.
func (l *lifecycleOperationLock) beginMutation() func() {
	if l == nil {
		return func() {}
	}
	l.mutationMu.Lock()
	if l.pendingMutations == 0 {
		l.mutationIdle = make(chan struct{})
	}
	l.pendingMutations++
	l.mutationMu.Unlock()

	l.interruptRevalidation()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mutationMu.Lock()
			l.pendingMutations--
			if l.pendingMutations == 0 {
				close(l.mutationIdle)
			}
			l.mutationMu.Unlock()
		})
	}
}

func (l *lifecycleOperationLock) waitMutationIdle(ctx context.Context) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		l.mutationMu.Lock()
		if l.pendingMutations == 0 {
			l.mutationMu.Unlock()
			return nil
		}
		idle := l.mutationIdle
		l.mutationMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idle:
		}
	}
}

func (l *lifecycleOperationLock) mutationPending() bool {
	if l == nil {
		return false
	}
	l.mutationMu.Lock()
	defer l.mutationMu.Unlock()
	return l.pendingMutations > 0
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
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := l.waitMutationIdle(ctx); err != nil {
			return err
		}
		if err := l.acquire(ctx); err != nil {
			return err
		}
		// A mutation may declare intent between the idle observation and token
		// acquisition. Yield and retry only after the complete mutation queue is
		// idle; do not consume-and-drop the revalidation trigger.
		if l.mutationPending() {
			l.release()
			continue
		}
		if err := ctx.Err(); err != nil {
			l.release()
			return err
		}
		fn()
		l.release()
		return nil
	}
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
