package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

// A failed exact rollback must not be mistaken for a restored session.
type protectedReplacementCleanupError struct{ cause error }

func (e protectedReplacementCleanupError) Error() string {
	return "Unable to switch VPN profiles: exact cleanup did not complete. Protection may remain armed; do not assume either profile is connected. Run podlaz status, podlaz debug doctor --tun and podlaz debug recover."
}

func (e protectedReplacementCleanupError) Unwrap() error { return e.cause }

// A transaction rollback can converge while the previous generation fails
// to reconnect or reverify. Never present that as successful restoration.
type protectedPreviousGenerationRestoreError struct{ cause error }

func (e protectedPreviousGenerationRestoreError) Error() string {
	return "Unable to finish VPN profile switching: the previous session's data plane could not be verified after rollback. Protection may remain armed; the connection is recovery-required. Run podlaz status, podlaz debug doctor --tun and podlaz debug recover."
}

func (e protectedPreviousGenerationRestoreError) Unwrap() error { return e.cause }

// Restoring a previous generation executes full-TUN connectivity verification,
// potentially twice. It needs a separate bounded budget from rollback.
const protectedPreviousGenerationRestoreTimeout = 2 * time.Minute

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

	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), protectedPreviousGenerationRestoreTimeout)
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
	if errors.Is(restoreErr, errConnectionAlreadyActive) {
		// An existing Xray child is not evidence that it is the previous
		// protected profile. Resolve the conflict from durable exact session
		// and verified current-generation evidence before accepting restore.
		if _, verified := l.verifiedSameNetworkSession(restoreCtx, previous, previous, true); verified {
			restoreErr = nil
		} else {
			restoreErr = fmt.Errorf("previous protected generation could not be verified after an active-core conflict: %w", restoreErr)
		}
	}
	cancel()

	l.continuationMu.Lock()
	stopped := l.explicitStop
	if l.restoreGeneration == generation {
		l.restoreCancel = nil
	}
	l.continuationMu.Unlock()

	if restoreErr == nil {
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
