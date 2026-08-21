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

func runBootAutostartStartup(
	ctx context.Context,
	manifestStore bootAutostartManifestStore,
	attemptStore bootAutostartAttemptStore,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	resume bootAutostartResumeFunc,
) (bootAutostartStartupResult, error) {
	if lifecycle == nil {
		return bootAutostartStartupBlocked, errors.New("boot autostart requires lifecycle service")
	}
	if resume == nil {
		return bootAutostartStartupBlocked, errors.New("boot autostart requires startup continuation function")
	}

	_, hadSession, err := continuation.stateStore().Load()
	if err != nil {
		return bootAutostartStartupRecoveryFailed, fmt.Errorf("inspect current Network Session before boot autostart: %w", err)
	}

	resumed, resumeErr := resume(ctx)
	if resumeErr != nil {
		return bootAutostartStartupRecoveryFailed, resumeErr
	}
	if hadSession || resumed {
		return completeBootAutostartAfterContinuation(attemptStore, resumed)
	}

	attempt, attemptExists, err := attemptStore.LoadCurrent()
	if err != nil {
		return bootAutostartStartupBlocked, fmt.Errorf("inspect boot autostart attempt: %w", err)
	}
	if attemptExists {
		switch attempt.State {
		case bootAutostartAttemptSucceeded, bootAutostartAttemptTerminal:
			return bootAutostartStartupNoop, nil
		case bootAutostartAttemptInProgress:
			return continueBootAutostartAttempt(ctx, attemptStore, continuation, lifecycle, attempt)
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
	return continueBootAutostartAttempt(ctx, attemptStore, continuation, lifecycle, attempt)
}

func completeBootAutostartAfterContinuation(attemptStore bootAutostartAttemptStore, resumed bool) (bootAutostartStartupResult, error) {
	attempt, exists, err := attemptStore.LoadCurrent()
	if err != nil {
		return bootAutostartStartupContinued, fmt.Errorf("inspect boot attempt after Network Session continuation: %w", err)
	}
	if !exists || attempt.State != bootAutostartAttemptInProgress {
		return bootAutostartStartupContinued, nil
	}
	if resumed {
		if err := attemptStore.MarkSucceeded(); err != nil {
			return bootAutostartStartupContinued, fmt.Errorf("complete resumed boot autostart attempt: %w", err)
		}
		return bootAutostartStartupContinued, nil
	}
	if err := attemptStore.MarkTerminal(bootAutostartTerminalSessionFailure); err != nil {
		return bootAutostartStartupTerminal, fmt.Errorf("complete terminal boot autostart continuation: %w", err)
	}
	return bootAutostartStartupTerminal, nil
}

func continueBootAutostartAttempt(
	ctx context.Context,
	attemptStore bootAutostartAttemptStore,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	attempt bootAutostartAttempt,
) (bootAutostartStartupResult, error) {
	request := api.ConnectRequest{Mode: attempt.Configuration.Mode, Profile: attempt.Configuration.Profile}
	_, connectErr := lifecycle.Connect(ctx, request)
	if connectErr == nil {
		if err := attemptStore.MarkSucceeded(); err != nil {
			return bootAutostartStartupBlocked, fmt.Errorf("complete successful boot autostart attempt: %w", err)
		}
		return bootAutostartStartupConnected, nil
	}

	_, sessionExists, stateErr := continuation.stateStore().Load()
	if stateErr != nil {
		return bootAutostartStartupBlocked, errors.Join(connectErr, fmt.Errorf("inspect Network Session after boot autostart failure: %w", stateErr))
	}
	if sessionExists {
		return bootAutostartStartupBlocked, connectErr
	}
	if err := attemptStore.MarkTerminal(bootAutostartTerminalConnectFailed); err != nil {
		return bootAutostartStartupTerminal, errors.Join(connectErr, fmt.Errorf("complete failed boot autostart attempt: %w", err))
	}
	return bootAutostartStartupTerminal, connectErr
}
