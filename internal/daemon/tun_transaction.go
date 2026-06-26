package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type tunPlanExecutor interface {
	Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error)
	Verify(context.Context, planner.TunPlan) error
	Rollback(context.Context, planner.TunPlan) error
}

type tunTransactionResult struct {
	TransactionID   string
	TransactionPath string
	Plan            planner.TunPlan
	Store           txstate.TransactionStore
}

func runTunTransaction(ctx context.Context, runtimeDir string, p profile.Profile, plan planner.TunPlan, executor tunPlanExecutor, now func() time.Time) (tunTransactionResult, error) {
	if executor == nil {
		return tunTransactionResult{}, errors.New("missing TUN executor")
	}
	if now == nil {
		now = time.Now
	}
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: now}
	tx := txstate.NewTransaction(newTunTransactionID(now), p.ID, planner.ModeTun, now())
	tx.BeforeSnapshot = snapshotMetadata(plan.Snapshot, now())
	tx.DesiredPlan = desiredPlanFromTunPlan(plan)
	path, err := store.Save(tx)
	if err != nil {
		return tunTransactionResult{}, err
	}

	if _, _, err := store.Transition(tx.ID, txstate.TransactionApplying); err != nil {
		return tunTransactionResult{}, err
	}
	tx, _, err = store.Load(tx.ID)
	if err != nil {
		return tunTransactionResult{}, err
	}
	steps, err := executor.Apply(ctx, plan)
	if err != nil {
		partialPlan := rollbackPlanFromAppliedSteps(plan, steps)
		return tunTransactionResult{}, rollbackTunFailure(ctx, store, &tx, partialPlan, executor, steps, fmt.Errorf("apply TUN plan: %w", err))
	}
	appliedPlan := rollbackPlanFromAppliedSteps(plan, steps)
	tx.AppliedSteps = appliedStepsFromExecutor(steps, now())
	tx.Rollback = rollbackMetadataFromTunPlan(appliedPlan)
	if _, err := store.Save(tx); err != nil {
		partialPlan := rollbackPlanFromAppliedSteps(plan, steps)
		return tunTransactionResult{}, rollbackTunFailure(ctx, store, &tx, partialPlan, executor, steps, fmt.Errorf("record applied TUN plan: %w", err))
	}
	if _, _, err := store.Transition(tx.ID, txstate.TransactionApplied); err != nil {
		partialPlan := rollbackPlanFromAppliedSteps(plan, steps)
		return tunTransactionResult{}, rollbackTunFailure(ctx, store, &tx, partialPlan, executor, steps, err)
	}
	if _, _, err := store.Transition(tx.ID, txstate.TransactionVerifying); err != nil {
		partialPlan := rollbackPlanFromAppliedSteps(plan, steps)
		return tunTransactionResult{}, rollbackTunFailure(ctx, store, &tx, partialPlan, executor, steps, err)
	}
	tx, _, err = store.Load(tx.ID)
	if err != nil {
		return tunTransactionResult{}, err
	}
	if err := executor.Verify(ctx, plan); err != nil {
		partialPlan := rollbackPlanFromAppliedSteps(plan, steps)
		return tunTransactionResult{}, rollbackTunFailure(ctx, store, &tx, partialPlan, executor, steps, fmt.Errorf("verify TUN plan: %w", err))
	}
	return tunTransactionResult{TransactionID: tx.ID, TransactionPath: path, Plan: plan, Store: store}, nil
}

func commitTunTransaction(store txstate.TransactionStore, transactionID string) error {
	if _, _, err := store.Transition(transactionID, txstate.TransactionCommitted); err != nil {
		return fmt.Errorf("commit TUN transaction %s: %w", transactionID, err)
	}
	return nil
}

func saveCoreRollbackMetadata(store txstate.TransactionStore, transactionID, runtimeConfigPath string, pid int, now time.Time) error {
	tx, _, err := store.Load(transactionID)
	if err != nil {
		return fmt.Errorf("load TUN transaction %s: %w", transactionID, err)
	}
	tx.DesiredPlan.Core = txstate.CorePlan{
		RuntimeConfigPath: runtimeConfigPath,
		ProcessLabel:      "xray",
		Owner:             txstate.TransactionOwner,
	}
	tx.Rollback.GeneratedConfigs = []txstate.GeneratedConfigRollback{{Path: runtimeConfigPath, Owner: txstate.TransactionOwner}}
	if pid > 0 {
		tx.Rollback.ChildProcesses = []txstate.ChildProcessRollback{{PID: pid, Label: "xray", ConfigRef: runtimeConfigPath, Owner: txstate.TransactionOwner}}
	}
	tx.Health = txstate.HealthResult{Status: "core-started", CheckedAt: now.UTC(), Message: "Xray process stayed alive during startup verification"}
	_, err = store.Save(tx)
	return err
}

func newTunTransactionID(now func() time.Time) string {
	return "tun-" + now().UTC().Format("20060102T150405.000000000Z")
}
