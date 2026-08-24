package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type networkSessionLifecycle struct {
	lifecycle       lifecycleService
	continuation    networkSessionContinuationStore
	terminalReasons *productTerminalReasonStore

	continuationMu    sync.Mutex
	explicitStop      bool
	restoreGeneration uint64
	restoreCancel     context.CancelFunc
}

func newNetworkSessionLifecycle(lifecycle lifecycleService, continuation networkSessionContinuationStore) *networkSessionLifecycle {
	return &networkSessionLifecycle{lifecycle: lifecycle, continuation: continuation}
}

func (l *networkSessionLifecycle) Connect(ctx context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	l.continuationMu.Lock()
	if l.explicitStop {
		l.continuationMu.Unlock()
		return api.LifecycleResponse{}, errLifecycleShuttingDown
	}
	previous, previousExists, err := l.continuation.LoadCurrent()
	if err != nil {
		l.continuationMu.Unlock()
		return api.LifecycleResponse{}, fmt.Errorf("load existing network session continuation before connect: %w", err)
	}
	if err := l.continuation.Save(request); err != nil {
		l.continuationMu.Unlock()
		return api.LifecycleResponse{}, err
	}
	replacementAttempt := previousExists && api.NormalizeHandoffPolicy(request.Handoff) == api.HandoffReplacePodlaz
	l.continuationMu.Unlock()

	// The durable Network Session save above is the lifecycle epoch admission
	// point. Request-facing startup/shutdown gates and the operation coordinator
	// reject before reaching it, so an unadmitted request cannot erase the last
	// valid product outcome. Once admitted, the new epoch supersedes that outcome
	// before any underlying networking mutation starts.
	if l.terminalReasons != nil {
		if err := l.terminalReasons.Supersede(); err != nil {
			restoreErr := l.restorePreviousContinuation(previous, previousExists)
			if restoreErr != nil {
				return api.LifecycleResponse{}, errors.Join(err, restoreErr)
			}
			return api.LifecycleResponse{}, err
		}
	}

	response, connectErr := l.lifecycle.Connect(ctx, request)
	if connectErr == nil {
		return response, nil
	}
	if restoreErr := l.restorePreviousContinuation(previous, previousExists); restoreErr != nil {
		return response, errors.Join(connectErr, restoreErr)
	}
	if replacementAttempt {
		if restoreErr := l.restorePreviousDataPlane(ctx, previous); restoreErr != nil {
			return response, errors.Join(connectErr, restoreErr)
		}
	}
	return response, connectErr
}

// ReconcileProtectedTun is the unwrapped automatic-repair entry point used only
// after the lifecycle operation lock has admitted and already owns the automatic
// mutation token. It does not acquire lifecycle serialization itself.
//
// The method first rechecks the durable Network Session identity and protection
// authority, then turns the current request into a one-shot replace-podlaz
// transition. Connect persists the exact previous request/protection as
// replacement rollback authority before the inner lifecycle is allowed to
// mutate the data plane.
func (l *networkSessionLifecycle) ReconcileProtectedTun(ctx context.Context, expectedSessionID string) error {
	if l == nil || l.lifecycle == nil {
		return errors.New("protected TUN reconciliation requires a lifecycle service")
	}
	expectedSessionID = strings.TrimSpace(expectedSessionID)
	if expectedSessionID == "" {
		return errors.New("protected TUN reconciliation requires a Network Session identity")
	}

	l.continuationMu.Lock()
	if l.explicitStop {
		l.continuationMu.Unlock()
		return errLifecycleShuttingDown
	}
	state, exists, err := l.continuation.stateStore().Load()
	if err != nil {
		l.continuationMu.Unlock()
		return fmt.Errorf("load Network Session before protected TUN reconciliation: %w", err)
	}
	if !exists || state.SessionID != expectedSessionID {
		l.continuationMu.Unlock()
		return errors.New("protected TUN reconciliation was superseded by another Network Session")
	}
	if state.Intent != networkSessionIntentResume {
		l.continuationMu.Unlock()
		return fmt.Errorf("protected TUN reconciliation cancelled by intent %q", state.Intent)
	}
	if state.Request.Mode != planner.ModeTun || state.Protection == nil {
		l.continuationMu.Unlock()
		return errors.New("protected TUN reconciliation requires a protected TUN Network Session")
	}
	if state.Replacement != nil {
		l.continuationMu.Unlock()
		return errors.New("protected TUN reconciliation replacement is already in progress")
	}
	request := state.Request
	request.Handoff = api.HandoffReplacePodlaz
	l.continuationMu.Unlock()

	_, err = l.Connect(ctx, request)
	if err != nil {
		return fmt.Errorf("reconcile protected TUN Network Session: %w", err)
	}
	return nil
}

func (l *networkSessionLifecycle) restorePreviousContinuation(previous api.ConnectRequest, exists bool) error {
	l.continuationMu.Lock()
	defer l.continuationMu.Unlock()
	if l.explicitStop {
		return nil
	}
	if exists {
		stateStore := l.continuation.stateStore()
		state, stateExists, err := stateStore.Load()
		if err != nil {
			return fmt.Errorf("load network session before failed replacement restore: %w", err)
		}
		if stateExists && state.Replacement != nil {
			if err := stateStore.RestoreReplacement(); err != nil {
				return fmt.Errorf("restore previous network session transition after failed connect: %w", err)
			}
		}
		if err := l.continuation.Save(previous); err != nil {
			return fmt.Errorf("restore previous network session continuation after failed connect: %w", err)
		}
		return nil
	}
	if err := l.continuation.Remove(); err != nil {
		return fmt.Errorf("disarm failed network session continuation: %w", err)
	}
	return nil
}

// disarmForExplicitStop makes service-stop intent terminal for this daemon
// instance before waiting for admitted lifecycle mutations to drain. Once set,
// a connect admitted before the shutdown fence cannot save or restore reconnect
// intent after the stop has been declared. Exact transaction and Privacy
// Envelope cleanup authority remain durable until final teardown succeeds.
func (l *networkSessionLifecycle) disarmForExplicitStop() error {
	l.continuationMu.Lock()
	defer l.continuationMu.Unlock()
	l.explicitStop = true
	l.cancelPreviousDataPlaneRestoreLocked()
	if err := l.continuation.disarm(networkSessionIntentDisconnect); err != nil {
		return fmt.Errorf("disarm network session continuation for explicit stop: %w", err)
	}
	return nil
}

func (l *networkSessionLifecycle) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	l.continuationMu.Lock()
	err := l.continuation.disarm(networkSessionIntentDisconnect)
	l.continuationMu.Unlock()
	if err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("disarm network session continuation before disconnect: %w", err)
	}

	response, disconnectErr := l.lifecycle.Disconnect(ctx)
	if disconnectErr != nil {
		return response, disconnectErr
	}

	l.continuationMu.Lock()
	finalizeErr := l.continuation.finalize()
	l.continuationMu.Unlock()
	if finalizeErr != nil {
		return response, fmt.Errorf("finalize network session state after disconnect: %w", finalizeErr)
	}
	return response, nil
}

func (l *networkSessionLifecycle) DisconnectForRestart(ctx context.Context) (api.LifecycleResponse, error) {
	return l.lifecycle.Disconnect(ctx)
}

type restartDisconnectLifecycle interface {
	DisconnectForRestart(context.Context) (api.LifecycleResponse, error)
}

type networkSessionStatusFunc func(context.Context) api.StatusResponse
type networkSessionRecoveryFunc func(context.Context, api.StatusResponse) api.RecoveryResponse
type networkSessionPrivacyReconcileStage func(context.Context, networkSessionStateStore) error
type networkSessionExactRecoveryStage func(context.Context, string) api.RecoveryResponse
type networkSessionTeardownRecoveryStage func(context.Context, networkSessionStateStore) error

func resumeNetworkSession(
	ctx context.Context,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	status networkSessionStatusFunc,
	recover networkSessionRecoveryFunc,
) (bool, error) {
	stateStore := continuation.stateStore()
	state, exists, err := stateStore.Load()
	if err != nil {
		return false, err
	}
	if !exists {
		migrated, migrateErr := migrateLegacyUpgradeContinuation(continuation.runtimeDir, continuation)
		if migrateErr != nil {
			return false, fmt.Errorf("migrate legacy package upgrade continuation: %w", migrateErr)
		}
		if migrated {
			state, exists, err = stateStore.Load()
			if err != nil {
				return false, err
			}
			if !exists {
				return false, errors.New("legacy package upgrade migration did not persist continuation")
			}
		}
	}

	reconcilePrivacy := continuation.reconcilePrivacy
	if reconcilePrivacy == nil {
		reconcilePrivacy = reconcileProductionNetworkSessionProtection
	}
	recoverExact := continuation.recoverExact
	if recoverExact == nil {
		recoverExact = recoverExactNetworkSessionTransactions
	}
	continueTeardown := continuation.continueTeardown
	if continueTeardown == nil {
		continueTeardown = continuePersistedNetworkSessionTeardown
	}

	if !exists {
		exactRecovery := recoverExact(ctx, continuation.runtimeDir)
		if !networkSessionRecoveryConverged(exactRecovery) {
			return false, errNetworkSessionRecoveryIncomplete
		}
		return false, nil
	}
	if status == nil || recover == nil {
		return false, errors.New("network session resume requires status and recovery functions")
	}

	switch state.Intent {
	case networkSessionIntentResume:
		if err := reconcilePrivacy(ctx, stateStore); err != nil {
			return false, fmt.Errorf("reconcile network session privacy protection: %w", err)
		}
		exactRecovery := recoverExact(ctx, continuation.runtimeDir)
		if !networkSessionRecoveryConverged(exactRecovery) {
			return false, errNetworkSessionRecoveryIncomplete
		}
		recovery := recover(ctx, status(ctx))
		if !networkSessionRecoveryConverged(recovery) {
			return false, errNetworkSessionRecoveryIncomplete
		}
		state, exists, err = stateStore.Load()
		if err != nil {
			return false, fmt.Errorf("reload Network Session before startup replay: %w", err)
		}
		if !exists {
			return false, errors.New("Network Session authority disappeared before startup replay")
		}
		if state.Intent != networkSessionIntentResume {
			return false, fmt.Errorf("Network Session startup replay cancelled by intent %q", state.Intent)
		}
		if _, err := lifecycle.Connect(ctx, state.Request); err != nil {
			return false, fmt.Errorf("resume network session: %w", err)
		}
		return true, nil

	case networkSessionIntentDisconnect, networkSessionIntentTerminal:
		// A persisted teardown decision is terminal for automatic continuation.
		// Keep the envelope in place while every exact/generic data-plane cleanup
		// stage converges, then deliberately remove protection, verify the
		// remaining host network, and clear the durable session authority.
		exactRecovery := recoverExact(ctx, continuation.runtimeDir)
		if !networkSessionRecoveryConverged(exactRecovery) {
			return false, errNetworkSessionRecoveryIncomplete
		}
		recovery := recover(ctx, status(ctx))
		if !networkSessionRecoveryConverged(recovery) {
			return false, errNetworkSessionRecoveryIncomplete
		}
		if err := continueTeardown(ctx, stateStore); err != nil {
			return false, fmt.Errorf("continue persisted network session teardown: %w", err)
		}
		return false, nil

	default:
		return false, fmt.Errorf("unsupported network session intent %q", state.Intent)
	}
}

func networkSessionRecoveryConverged(response api.RecoveryResponse) bool {
	if len(response.Warnings) != 0 {
		return false
	}
	for _, result := range response.Results {
		if result.Status != "recovered" {
			return false
		}
	}
	return true
}
