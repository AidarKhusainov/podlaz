package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type networkSessionPrivacyReconcileStage func(context.Context, networkSessionStateStore) error
type networkSessionExactRecoveryStage func(context.Context, string) api.RecoveryResponse

// resumeNetworkSessionWithRecoveryStages keeps the complete protected startup
// sequence explicit and testable. Privacy reconciliation runs before any exact
// data-plane rollback so a previously protected Network Session cannot open a
// direct-routing gap during daemon/service/package continuation.
func resumeNetworkSessionWithRecoveryStages(
	ctx context.Context,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	status networkSessionStatusFunc,
	recover networkSessionRecoveryFunc,
	reconcilePrivacy networkSessionPrivacyReconcileStage,
	recoverExact networkSessionExactRecoveryStage,
) (bool, error) {
	request, exists, err := continuation.LoadCurrent()
	if err != nil {
		return false, err
	}
	if !exists {
		migrated, migrateErr := migrateLegacyUpgradeContinuation(continuation.runtimeDir, continuation)
		if migrateErr != nil {
			return false, fmt.Errorf("migrate legacy package upgrade continuation: %w", migrateErr)
		}
		if migrated {
			request, exists, err = continuation.LoadCurrent()
			if err != nil {
				return false, err
			}
			if !exists {
				return false, errors.New("legacy package upgrade migration did not persist continuation")
			}
		}
	}

	if reconcilePrivacy == nil || recoverExact == nil {
		return false, errors.New("network session resume requires privacy and exact recovery stages")
	}
	if err := reconcilePrivacy(ctx, continuation.stateStore()); err != nil {
		return false, fmt.Errorf("reconcile network session privacy protection: %w", err)
	}

	exactRecovery := recoverExact(ctx, continuation.runtimeDir)
	if !networkSessionRecoveryConverged(exactRecovery) {
		return false, errNetworkSessionRecoveryIncomplete
	}
	if !exists {
		return false, nil
	}
	if status == nil || recover == nil {
		return false, errors.New("network session resume requires status and recovery functions")
	}
	recovery := recover(ctx, status(ctx))
	if !networkSessionRecoveryConverged(recovery) {
		return false, errNetworkSessionRecoveryIncomplete
	}
	if _, err := lifecycle.Connect(ctx, request); err != nil {
		return false, fmt.Errorf("resume network session: %w", err)
	}
	return true, nil
}

func resumeProtectedNetworkSession(
	ctx context.Context,
	continuation networkSessionContinuationStore,
	lifecycle lifecycleService,
	status networkSessionStatusFunc,
	recover networkSessionRecoveryFunc,
) (bool, error) {
	return resumeNetworkSessionWithRecoveryStages(
		ctx,
		continuation,
		lifecycle,
		status,
		recover,
		reconcileProductionNetworkSessionProtection,
		recoverExactNetworkSessionTransactions,
	)
}
