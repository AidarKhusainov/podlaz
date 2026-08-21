package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type bootAutostartStartupResult string

const (
	bootAutostartStartupNoop           bootAutostartStartupResult = "noop"
	bootAutostartStartupContinued      bootAutostartStartupResult = "continued"
	bootAutostartStartupConnected      bootAutostartStartupResult = "connected"
	bootAutostartStartupTerminal       bootAutostartStartupResult = "terminal"
	bootAutostartStartupBlocked        bootAutostartStartupResult = "blocked"
	bootAutostartStartupRecoveryFailed bootAutostartStartupResult = "recovery_failed"
)

type bootAutostartResumeFunc func(context.Context) (bool, error)
type bootAutostartTerminalConvergeFunc func(context.Context, networkSessionContinuationStore) error
type bootAutostartNetworkReadyFunc func(context.Context) error

type bootAutostartStartupOptions struct {
	terminalConverge bootAutostartTerminalConvergeFunc
	waitForNetwork   bootAutostartNetworkReadyFunc
}

func runBootAutostartStartup(
	ctx context.Context,
	manifestStore bootAutostartManifestStore,
	attemptStore bootAutostartAttemptStore,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	resume bootAutostartResumeFunc,
	terminalConverge ...bootAutostartTerminalConvergeFunc,
) (bootAutostartStartupResult, error) {
	if len(terminalConverge) > 1 {
		return bootAutostartStartupBlocked, errors.New("boot autostart accepts at most one terminal convergence function")
	}
	options := bootAutostartStartupOptions{}
	if len(terminalConverge) == 1 {
		options.terminalConverge = terminalConverge[0]
	}
	return runBootAutostartStartupWithOptions(ctx, manifestStore, attemptStore, continuation, lifecycle, resume, options)
}

func runBootAutostartStartupWithOptions(
	ctx context.Context,
	manifestStore bootAutostartManifestStore,
	attemptStore bootAutostartAttemptStore,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	resume bootAutostartResumeFunc,
	options bootAutostartStartupOptions,
) (bootAutostartStartupResult, error) {
	if lifecycle == nil {
		return bootAutostartStartupBlocked, errors.New("boot autostart requires lifecycle service")
	}
	if resume == nil {
		return bootAutostartStartupBlocked, errors.New("boot autostart requires startup continuation function")
	}
	convergeTerminal := options.terminalConverge
	if convergeTerminal == nil {
		convergeTerminal = defaultBootAutostartTerminalConverge
	}

	state, hadSession, err := continuation.stateStore().Load()
	if err != nil {
		return bootAutostartStartupRecoveryFailed, fmt.Errorf("inspect current Network Session before boot autostart: %w", err)
	}
	attempt, attemptExists, err := attemptStore.LoadCurrent()
	if err != nil {
		return bootAutostartStartupBlocked, fmt.Errorf("inspect boot autostart attempt: %w", err)
	}

	if hadSession {
		if attemptExists && attempt.State == bootAutostartAttemptInProgress && state.Intent == networkSessionIntentTerminal {
			return convergeAndCompleteBootAutostartTerminal(ctx, attemptStore, continuation, convergeTerminal, bootAutostartTerminalSessionFailure)
		}
		resumed, resumeErr := resume(ctx)
		if resumeErr != nil {
			return bootAutostartStartupRecoveryFailed, resumeErr
		}
		if attemptExists && attempt.State == bootAutostartAttemptInProgress && resumed {
			if err := attemptStore.MarkSucceeded(); err != nil {
				return bootAutostartStartupRecoveryFailed, fmt.Errorf("complete resumed boot autostart attempt: %w", err)
			}
		}
		return bootAutostartStartupContinued, nil
	}

	// Even without a Network Session record, exact orphan transaction recovery
	// from the existing lifecycle must run before any fresh boot admission.
	resumed, resumeErr := resume(ctx)
	if resumeErr != nil {
		return bootAutostartStartupRecoveryFailed, resumeErr
	}
	if resumed {
		return bootAutostartStartupContinued, nil
	}

	if attemptExists {
		switch attempt.State {
		case bootAutostartAttemptSucceeded, bootAutostartAttemptTerminal:
			return bootAutostartStartupNoop, nil
		case bootAutostartAttemptInProgress:
			return continueBootAutostartAttempt(ctx, attemptStore, continuation, lifecycle, attempt, convergeTerminal, options.waitForNetwork)
		default:
			return bootAutostartStartupBlocked, fmt.Errorf("unsupported boot autostart attempt state %q", attempt.State)
		}
	}

	manifest, exists, err := manifestStore.Load()
	if err != nil {
		return bootAutostartStartupBlocked, fmt.Errorf("load boot autostart manifest: %w", err)
	}
	if !exists {
		return bootAutostartStartupNoop, nil
	}
	currentBootID, err := requiredBootID(attemptStore.readBootID, "boot autostart startup")
	if err != nil {
		return bootAutostartStartupBlocked, err
	}
	if !manifest.EligibleForBoot(currentBootID) {
		return bootAutostartStartupNoop, nil
	}

	attempt, err = attemptStore.Admit(manifest)
	if err != nil {
		return bootAutostartStartupBlocked, fmt.Errorf("admit boot autostart attempt: %w", err)
	}
	return continueBootAutostartAttempt(ctx, attemptStore, continuation, lifecycle, attempt, convergeTerminal, options.waitForNetwork)
}

func continueBootAutostartAttempt(
	ctx context.Context,
	attemptStore bootAutostartAttemptStore,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	attempt bootAutostartAttempt,
	convergeTerminal bootAutostartTerminalConvergeFunc,
	waitForNetwork bootAutostartNetworkReadyFunc,
) (bootAutostartStartupResult, error) {
	request := api.ConnectRequest{Mode: attempt.Configuration.Mode, Profile: attempt.Configuration.Profile}

	// Readiness is part of the already-admitted logical attempt, not a retry of
	// Connect. A replacement daemon can re-enter this bounded wait while the
	// attempt remains in_progress, but Connect is invoked only after fresh usable
	// uplink evidence is present.
	if waitForNetwork != nil {
		if err := waitForNetwork(ctx); err != nil {
			if ctx.Err() != nil {
				return bootAutostartStartupContinued, err
			}
			if errors.Is(err, errBootAutostartNetworkNotReady) {
				return completeBootAutostartBeforeConnectFailure(ctx, attemptStore, continuation, request, err)
			}
			return bootAutostartStartupRecoveryFailed, fmt.Errorf("inspect boot network readiness: %w", err)
		}
	}

	// Arm the same #259 Network Session authority before entering normal Connect.
	// That closes both sides of the crash window: a crash before this save can
	// replay the pinned attempt, while every crash after it is continuation work.
	if err := continuation.Save(request); err != nil {
		return bootAutostartStartupRecoveryFailed, fmt.Errorf("arm Network Session before boot autostart connect: %w", err)
	}

	_, connectErr := lifecycle.Connect(ctx, request)
	if connectErr == nil {
		if err := attemptStore.MarkSucceeded(); err != nil {
			return bootAutostartStartupRecoveryFailed, fmt.Errorf("complete successful boot autostart attempt: %w", err)
		}
		return bootAutostartStartupConnected, nil
	}

	if ctx.Err() != nil {
		// Only cancellation/deadline of the authoritative startup context means
		// daemon replacement/interruption. Connect can legitimately wrap child
		// context deadlines from route/TCP/DNS probes while this parent remains
		// live; those are ordinary connection failures and must converge below.
		return bootAutostartStartupContinued, connectErr
	}

	if err := continuation.disarm(networkSessionIntentTerminal); err != nil {
		return bootAutostartStartupRecoveryFailed, errors.Join(connectErr, fmt.Errorf("persist terminal Network Session intent after boot connect failure: %w", err))
	}
	result, terminalErr := convergeAndCompleteBootAutostartTerminal(
		ctx,
		attemptStore,
		continuation,
		convergeTerminal,
		bootAutostartTerminalConnectFailed,
	)
	if terminalErr != nil {
		return result, errors.Join(connectErr, terminalErr)
	}
	return result, connectErr
}

func completeBootAutostartBeforeConnectFailure(
	ctx context.Context,
	attemptStore bootAutostartAttemptStore,
	continuation networkSessionContinuationStore,
	request api.ConnectRequest,
	cause error,
) (bootAutostartStartupResult, error) {
	// No Podlaz networking mutation has happened yet, so exact teardown is not
	// required. Still create terminal Network Session authority before committing
	// attempt=terminal: if that completion write/fsync fails, a replacement daemon
	// sees terminal intent and cannot replay a fresh same-boot Connect.
	if err := continuation.Save(request); err != nil {
		return bootAutostartStartupRecoveryFailed, errors.Join(cause, fmt.Errorf("arm fail-closed Network Session after boot readiness timeout: %w", err))
	}
	if err := continuation.disarm(networkSessionIntentTerminal); err != nil {
		return bootAutostartStartupRecoveryFailed, errors.Join(cause, fmt.Errorf("persist terminal Network Session after boot readiness timeout: %w", err))
	}
	if err := attemptStore.MarkTerminal(bootAutostartTerminalNetworkNotReady); err != nil {
		return bootAutostartStartupRecoveryFailed, errors.Join(cause, fmt.Errorf("persist boot network readiness terminal outcome: %w", err))
	}
	if err := continuation.finalize(); err != nil {
		return bootAutostartStartupTerminal, errors.Join(cause, fmt.Errorf("clear boot readiness terminal authority: %w", err))
	}
	return bootAutostartStartupTerminal, cause
}

func convergeAndCompleteBootAutostartTerminal(
	ctx context.Context,
	attemptStore bootAutostartAttemptStore,
	continuation networkSessionContinuationStore,
	convergeTerminal bootAutostartTerminalConvergeFunc,
	reason bootAutostartTerminalReason,
) (bootAutostartStartupResult, error) {
	if convergeTerminal == nil {
		return bootAutostartStartupRecoveryFailed, errors.New("boot autostart terminal convergence is unavailable")
	}
	if err := convergeTerminal(ctx, continuation); err != nil {
		return bootAutostartStartupRecoveryFailed, fmt.Errorf("converge boot autostart terminal cleanup: %w", err)
	}

	if err := attemptStore.MarkTerminal(reason); err != nil {
		return bootAutostartStartupRecoveryFailed, fmt.Errorf("persist terminal boot autostart completion: %w", err)
	}
	if err := continuation.finalize(); err != nil {
		return bootAutostartStartupTerminal, fmt.Errorf("clear converged boot autostart Network Session authority: %w", err)
	}
	return bootAutostartStartupTerminal, nil
}

func defaultBootAutostartTerminalConverge(ctx context.Context, continuation networkSessionContinuationStore) error {
	recoverExact := continuation.recoverExact
	if recoverExact == nil {
		recoverExact = recoverExactNetworkSessionTransactions
	}
	exactRecovery := recoverExact(ctx, continuation.runtimeDir)
	if !networkSessionRecoveryConverged(exactRecovery) {
		return errNetworkSessionRecoveryIncomplete
	}

	genericRecovery := daemonRecover(ctx, continuation.runtimeDir, api.StatusResponse{Connection: "inactive"})
	if !networkSessionRecoveryConverged(genericRecovery) {
		return errNetworkSessionRecoveryIncomplete
	}

	return convergePersistedNetworkSessionTeardown(ctx, continuation.stateStore())
}
