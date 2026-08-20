package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

// restorePreviousDataPlane gives a failed protected replacement one bounded,
// daemon-owned opportunity to restore the previous generation. It deliberately
// ignores cancellation of the request that attempted the replacement: the old
// generation may already have been torn down, while the session-wide Privacy
// Envelope still blocks ordinary direct egress.
//
// Registration and explicit-stop cancellation share continuationMu. This closes
// the check-then-start race where shutdown could declare terminal intent after a
// restore eligibility check but before Connect(previous) actually began.
func (l *networkSessionLifecycle) restorePreviousDataPlane(ctx context.Context, previous api.ConnectRequest) error {
	if l == nil || l.lifecycle == nil {
		return errors.New("restore previous data plane requires a lifecycle service")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tunRollbackCleanupTimeout)
	l.continuationMu.Lock()
	if l.explicitStop {
		l.continuationMu.Unlock()
		cancel()
		return nil
	}
	l.restoreGeneration++
	generation := l.restoreGeneration
	l.restoreCancel = cancel
	l.continuationMu.Unlock()

	_, restoreErr := l.lifecycle.Connect(restoreCtx, previous)
	cancel()

	l.continuationMu.Lock()
	stopped := l.explicitStop
	if l.restoreGeneration == generation {
		l.restoreCancel = nil
	}
	l.continuationMu.Unlock()

	if restoreErr == nil || errors.Is(restoreErr, errConnectionAlreadyActive) {
		return nil
	}
	if stopped && errors.Is(restoreErr, context.Canceled) {
		// Explicit stop owns the terminal teardown from this point. Do not turn
		// its intentional cancellation into a second replacement failure.
		return nil
	}
	return fmt.Errorf("restore previous data plane after failed replacement: %w", restoreErr)
}

func (l *networkSessionLifecycle) cancelPreviousDataPlaneRestoreLocked() {
	if l.restoreCancel == nil {
		return
	}
	l.restoreCancel()
	l.restoreCancel = nil
	l.restoreGeneration++
}
