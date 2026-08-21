package daemon

import (
	"context"
	"errors"
	"sync"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

var errLifecycleShuttingDown = errors.New("daemon shutdown is in progress")

type lifecycleOperationLock struct {
	token chan struct{}

	mutationMu         sync.Mutex
	pendingMutations   int
	mutationGeneration uint64
	mutationIdle       chan struct{}
	mutationsClosed    bool

	cancelMu           sync.RWMutex
	cancelRevalidation context.CancelFunc
}

type lifecycleMutationState struct {
	generation uint64
	pending    bool
	fenced     bool
}

type lifecycleAutomaticAdmission struct {
	lock *lifecycleOperationLock
	once sync.Once
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

// beginMutation declares internal mutation intent before cancelling any active
// probe. It deliberately bypasses the shutdown fence so deterministic internal
// tests and shutdown-owned final teardown can still use the same accounting
// primitive. Request-facing lifecycle paths must use beginExternalMutation.
func (l *lifecycleOperationLock) beginMutation() func() {
	finish, _ := l.beginMutationWithFence(false)
	return finish
}

// beginExternalMutation atomically rejects mutations declared after the daemon
// has entered shutdown. The fence check and pending-mutation registration share
// mutationMu, so shutdown cannot observe the queue drained while a request has
// already been admitted but not yet counted.
func (l *lifecycleOperationLock) beginExternalMutation() (func(), error) {
	return l.beginMutationWithFence(true)
}

func (l *lifecycleOperationLock) beginMutationWithFence(rejectClosed bool) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	l.mutationMu.Lock()
	if rejectClosed && l.mutationsClosed {
		l.mutationMu.Unlock()
		return nil, errLifecycleShuttingDown
	}
	l.mutationGeneration++
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
	}, nil
}

// lifecycleMutationSnapshot captures the lifecycle generation visible to a
// caller that already holds whatever operation authority its protocol requires.
// In particular, read-only revalidation captures this only after runRevalidation
// owns the operation token and has rechecked that no mutation is pending.
func (l *lifecycleOperationLock) lifecycleMutationSnapshot() lifecycleMutationState {
	if l == nil {
		return lifecycleMutationState{}
	}
	l.mutationMu.Lock()
	defer l.mutationMu.Unlock()
	return lifecycleMutationState{
		generation: l.mutationGeneration,
		pending:    l.pendingMutations > 0,
		fenced:     l.mutationsClosed,
	}
}

// tryAdmitAutomaticMutation is the linearization point for a post-revalidation
// reconcile/terminal mutation. It never waits for operation authority: while
// mutationMu is held it proves the observed lifecycle generation is still
// current, that no earlier mutation has been declared, and atomically takes the
// existing operation token before the admission can succeed.
func (l *lifecycleOperationLock) tryAdmitAutomaticMutation(expectedGeneration uint64) (*lifecycleAutomaticAdmission, bool) {
	if l == nil {
		return &lifecycleAutomaticAdmission{}, true
	}
	l.mutationMu.Lock()
	defer l.mutationMu.Unlock()

	if l.mutationsClosed || l.pendingMutations != 0 || l.mutationGeneration != expectedGeneration {
		return nil, false
	}
	select {
	case <-l.token:
		// Operation authority is now owned while mutation ordering is still
		// serialized by mutationMu.
	default:
		return nil, false
	}

	l.mutationIdle = make(chan struct{})
	l.pendingMutations = 1
	l.mutationGeneration++
	return &lifecycleAutomaticAdmission{lock: l}, true
}

// Release completes one automatic mutation registration and returns its already
// owned operation token exactly once. Later explicit mutations may have declared
// themselves while the automatic mutation was running; they remain pending and
// can acquire the token only after it is returned here.
func (a *lifecycleAutomaticAdmission) Release() {
	if a == nil {
		return
	}
	a.once.Do(func() {
		if a.lock == nil {
			return
		}
		l := a.lock
		l.mutationMu.Lock()
		l.pendingMutations--
		if l.pendingMutations == 0 {
			close(l.mutationIdle)
		}
		l.mutationMu.Unlock()
		l.release()
	})
}

// fenceMutations rejects every lifecycle mutation declared after this point.
// Already-declared mutations remain counted and are drained through
// waitMutationIdle before shutdown runs its single final teardown.
func (l *lifecycleOperationLock) fenceMutations() {
	if l == nil {
		return
	}
	l.mutationMu.Lock()
	l.mutationsClosed = true
	l.mutationMu.Unlock()
	l.interruptRevalidation()
}

func (l *lifecycleOperationLock) mutationsFenced() bool {
	if l == nil {
		return false
	}
	l.mutationMu.Lock()
	defer l.mutationMu.Unlock()
	return l.mutationsClosed
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
	return l.runRecoveryWithFollowUp(ctx, fn, nil)
}

// runRecoveryWithFollowUp keeps recovery and any startup-resume follow-up under
// one mutation registration and one operation token. This prevents a second
// recovery request from observing or rolling back state created by the first
// request's automatic resume before that lifecycle transition is complete.
func (l *lifecycleOperationLock) runRecoveryWithFollowUp(
	ctx context.Context,
	fn func() api.RecoveryResponse,
	followUp func(api.RecoveryResponse) api.RecoveryResponse,
) api.RecoveryResponse {
	if l == nil {
		response := fn()
		if followUp != nil {
			return followUp(response)
		}
		return response
	}
	finishMutation, err := l.beginExternalMutation()
	if err != nil {
		return lifecycleOperationRecoveryError(err)
	}
	defer finishMutation()
	if err := l.acquire(ctx); err != nil {
		return lifecycleOperationRecoveryError(err)
	}
	defer l.release()
	response := fn()
	if followUp != nil {
		return followUp(response)
	}
	return response
}

func lifecycleOperationRecoveryError(err error) api.RecoveryResponse {
	return api.RecoveryResponse{
		Mode: "execute",
		Warnings: []api.RecoveryWarning{{
			Target:  "lifecycle operation",
			Message: err.Error(),
		}},
	}
}

type operationLockedLifecycle struct {
	lock      *lifecycleOperationLock
	lifecycle lifecycleService
}

func (l operationLockedLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	finishMutation, err := l.lock.beginExternalMutation()
	if err != nil {
		return api.LifecycleResponse{}, err
	}
	defer finishMutation()
	if err := l.lock.acquire(ctx); err != nil {
		return api.LifecycleResponse{}, err
	}
	defer l.lock.release()
	return l.lifecycle.Connect(ctx, request)
}

func (l operationLockedLifecycle) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	finishMutation, err := l.lock.beginExternalMutation()
	if err != nil {
		return api.LifecycleResponse{}, err
	}
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
