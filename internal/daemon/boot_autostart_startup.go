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

func runBootAutostartStartup(
	ctx context.Context,
	manifestStore bootAutostartManifestStore,
	attemptStore bootAutostartAttemptStore,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	resume bootAutostartResumeFunc,
	terminalConverge ...bootAutostartTerminalConvergeFunc,
) (bootAutostartStartupResult, error) {
	if lifecycle == nil {
		return bootAutostartStartupBlocked, errors.New("boot autostart requires lifecycle service")
	}
	if resume == nil {
		return bootAutostartStartupBlocked, errors.New("boot autostart requires startup continuation function")
	}
	if len(terminalConverge) > 1 {
		return bootAutostartStartupBlocked, errors.New("boot autostart accepts at most one terminal convergence function")
	}
	convergeTerminal := bootAutostartTerminalConvergeFunc(defaultBootAutostartTerminalConverge)
	if len(terminalConverge) == 1 {
		if terminalConverge[0] == nil {
			return bootAutostartStartupBlocked, errors.New("boot autostart terminal convergence function is nil")
		}
		convergeTerminal = terminalConverge[0]
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
			return continueBootAutostartAttempt(ctx, attemptStore, continuation, lifecycle, attempt, convergeTerminal)
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
	return continueBootAutostartAttempt(ctx, attemptStore, continuation, lifecycle, attempt, convergeTerminal)
}

func continueBootAutostartAttempt(
	ctx context.Context,
	attemptStore bootAutostartAttemptStore,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	attempt bootAutostartAttempt,
	convergeTerminal bootAutostartTerminalConvergeFunc,
) (bootAutostartStartupResult, error) {
	request := api.ConnectRequest{Mode: attempt.Configuration.Mode, Profile: attempt.Configuration.Profile}

	// Arm the same #259 Network Session authority before entering normal Connect.
	// That closes both sides of the crash window: a crash before this save can
	// replay the pinned attempt, while every crash after it is continuation work.
	// networkSessionLifecycle.Connect treats this identical request as the
	// existing session, so a returned Connect error restores rather than removes
	// the authority.
	if err := continuation.Save(request); err != nil {
		return bootAutostartStartupRecoveryFailed, fmt.Errorf("arm Network Session before boot autostart connect: %w", err)
	}

	_, connectErr := lifecycle.Connect(ctx, request)
	if connectErr == nil {
		if err := attemptStore.MarkSucceeded(); err != nil {
			// Do not remove the resume-capable Network Session. A completion write
			// failure must leave the next daemon fail-closed on continuation rather
			// than replaying a fresh automatic Connect.
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

	// Terminal is publishable only after exact/generic cleanup and remaining
	// host-network verification have converged. Keep terminal Network Session
	// authority until this durable attempt transition succeeds, so a failed
	// write/fsync cannot reopen same-boot automatic Connect authority.
	if err := attemptStore.MarkTerminal(reason); err != nil {
		return bootAutostartStartupRecoveryFailed, fmt.Errorf("persist terminal boot autostart completion: %w", err)
	}
	if err := continuation.finalize(); err != nil {
		// The attempt is already terminal and therefore fail-closed. Retaining a
		// converged terminal continuation is safe and lets normal startup recovery
		// finish authority cleanup later.
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

	// An admitted boot attempt has already been declared terminal at this point.
	// Generic recovery therefore operates on inactive product semantics: it may
	// clean only scanner-proven Podlaz-owned stale resources and never preserves
	// an active session merely because its failed Connect left runtime evidence.
	genericRecovery := daemonRecover(ctx, continuation.runtimeDir, api.StatusResponse{Connection: "inactive"})
	if !networkSessionRecoveryConverged(genericRecovery) {
		return errNetworkSessionRecoveryIncomplete
	}

	return convergePersistedNetworkSessionTeardown(ctx, continuation.stateStore())
}
