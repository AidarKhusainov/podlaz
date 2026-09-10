package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type networkSessionResumeResult string

const (
	networkSessionResumeUnknown           networkSessionResumeResult = ""
	networkSessionResumeNoSession         networkSessionResumeResult = "no-session"
	networkSessionResumeResumed           networkSessionResumeResult = "resumed"
	networkSessionResumeTerminalConverged networkSessionResumeResult = "terminal-converged"
)

// resumeNetworkSessionResult is the semantic caller boundary for startup and
// explicit recovery. The lower-level boolean helper reports whether Connect
// replay succeeded; this adapter distinguishes retained terminal convergence
// from the absence of Network Session authority for callers with different
// durable finalization responsibilities.
func resumeNetworkSessionResult(
	ctx context.Context,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	status networkSessionStatusFunc,
	recover networkSessionRecoveryFunc,
) (networkSessionResumeResult, error) {
	return resumeNetworkSessionResultWithTerminalObservation(ctx, continuation, lifecycle, status, recover, nil)
}

func resumeNetworkSessionResultWithTerminalObservation(
	ctx context.Context,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	status networkSessionStatusFunc,
	recover networkSessionRecoveryFunc,
	observeTerminal networkSessionTerminalObservationStage,
) (networkSessionResumeResult, error) {
	retained := continuation
	if retained.continueTeardown == nil {
		retained.continueTeardown = convergePersistedNetworkSessionTeardown
	}

	resumed, err := resumeNetworkSessionWithTerminalObservation(
		ctx,
		retained,
		lifecycle,
		status,
		recover,
		observeTerminal,
	)
	if err != nil {
		return networkSessionResumeUnknown, err
	}
	if resumed {
		return networkSessionResumeResumed, nil
	}

	state, exists, err := retained.stateStore().Load()
	if err != nil {
		return networkSessionResumeUnknown, fmt.Errorf("inspect Network Session after resume convergence: %w", err)
	}
	if !exists {
		return networkSessionResumeNoSession, nil
	}
	if state.Intent == networkSessionIntentDisconnect || state.Intent == networkSessionIntentTerminal {
		// A successful teardown stage is the semantic proof. Production teardown
		// removes session protection before returning; tests may inject an
		// equivalent successful stage without reproducing its internal writes.
		return networkSessionResumeTerminalConverged, nil
	}
	return networkSessionResumeUnknown, errors.New("network session resume completed without a terminal semantic result")
}

func finalizeNetworkSessionResumeResult(
	continuation networkSessionContinuationStore,
	result networkSessionResumeResult,
) error {
	if result != networkSessionResumeTerminalConverged {
		return nil
	}
	if err := continuation.finalize(); err != nil {
		return fmt.Errorf("finalize converged terminal Network Session: %w", err)
	}
	return nil
}

func networkSessionResumeRecoveryResponseError(err error) api.RecoveryResponse {
	return lifecycleOperationRecoveryError(err)
}
