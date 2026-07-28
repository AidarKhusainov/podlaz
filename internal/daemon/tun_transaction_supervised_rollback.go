package daemon

import (
	"context"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func (e *tunNetworkMutationError) RollbackWithChildStopper(ctx context.Context, executor tunPlanExecutor, stopChildren tunRollbackChildStopper) error {
	if e == nil {
		return nil
	}
	return rollbackPreparedTunFailureWithChildStopper(ctx, e.store, &e.transaction, e.rollbackPlan, executor, stopChildren)
}

func rollbackVerifiedTunTransactionWithChildStopper(ctx context.Context, runtimeDir, transactionID string, plan planner.TunPlan, executor tunPlanExecutor, stopChildren tunRollbackChildStopper) error {
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx, _, err := store.Load(transactionID)
	if err != nil {
		return fmt.Errorf("load TUN transaction %s: %w", transactionID, err)
	}
	rollbackPlan := rollbackPlanFromPersistedTransaction(plan, tx)
	return rollbackPreparedTunFailureWithChildStopper(ctx, store, &tx, rollbackPlan, executor, stopChildren)
}
