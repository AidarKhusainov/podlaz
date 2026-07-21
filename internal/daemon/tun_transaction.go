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

// tunNetworkMutationError preserves the exact in-memory rollback context after
// podlaz has started mutating host networking. The full-tunnel runner can use
// this boundary to collect diagnostics before cleanup without weakening the
// direct transaction helper's immediate-rollback contract.
type tunNetworkMutationError struct {
	phase        string
	store        txstate.TransactionStore
	transaction  txstate.Transaction
	rollbackPlan planner.TunPlan
	steps        []netexecutor.Step
	cause        error
}

func (e *tunNetworkMutationError) Error() string {
	if e == nil || e.cause == nil {
		return "TUN network mutation failed"
	}
	return e.cause.Error()
}

func (e *tunNetworkMutationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *tunNetworkMutationError) Phase() string {
	if e == nil {
		return ""
	}
	return e.phase
}

func (e *tunNetworkMutationError) Rollback(ctx context.Context, executor tunPlanExecutor) error {
	if e == nil {
		return nil
	}
	return rollbackPreparedTunFailure(ctx, e.store, &e.transaction, e.rollbackPlan, executor)
}

func runTunTransaction(ctx context.Context, runtimeDir string, p profile.Profile, plan planner.TunPlan, executor tunPlanExecutor, now func() time.Time) (tunTransactionResult, error) {
	if executor == nil {
		return tunTransactionResult{}, errors.New("missing TUN executor")
	}
	result, err := beginTunTransaction(ctx, runtimeDir, p, plan, now)
	if err != nil {
		return tunTransactionResult{}, err
	}
	if err := applyVerifyTunTransaction(ctx, result, executor); err != nil {
		return tunTransactionResult{}, err
	}
	return result, nil
}

func beginTunTransaction(_ context.Context, runtimeDir string, p profile.Profile, plan planner.TunPlan, now func() time.Time) (tunTransactionResult, error) {
	if now == nil {
		now = time.Now
	}
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: now}
	tx := txstate.NewTransaction(newTunTransactionID(now), p.ID, planner.ModeTun, now())
	tx.BeforeSnapshot = snapshotMetadata(plan.Snapshot, now())
	tx.DesiredPlan = desiredPlanFromTunPlan(plan)
	tx.Labels = tunTransactionDiagnosticLabels(p)
	path, err := store.Save(tx)
	if err != nil {
		return tunTransactionResult{}, err
	}

	if _, _, err := store.Transition(tx.ID, txstate.TransactionApplying); err != nil {
		return tunTransactionResult{}, err
	}
	return tunTransactionResult{TransactionID: tx.ID, TransactionPath: path, Plan: plan, Store: store}, nil
}

// applyVerifyTunTransaction keeps the direct transaction helper fail-safe: any
// post-mutation failure is rolled back before this function returns.
func applyVerifyTunTransaction(ctx context.Context, result tunTransactionResult, executor tunPlanExecutor) error {
	err := applyVerifyTunTransactionDeferredRollback(ctx, result, executor)
	if err == nil {
		return nil
	}
	var mutationErr *tunNetworkMutationError
	if !errors.As(err, &mutationErr) {
		return err
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tunRollbackCleanupTimeout)
	defer cancel()
	if rollbackErr := mutationErr.Rollback(cleanupCtx, executor); rollbackErr != nil {
		return errors.Join(err, fmt.Errorf("rollback TUN plan: %w", rollbackErr))
	}
	return fmt.Errorf("%w; rolled back applied podlaz-owned TUN, route, policy-rule, DNS, and nftables state", err)
}

// applyVerifyTunTransactionDeferredRollback records exact rollback ownership but
// deliberately leaves cleanup to the caller. Production full-tunnel lifecycle
// uses this variant so diagnostics can observe the failed applied state.
func applyVerifyTunTransactionDeferredRollback(ctx context.Context, result tunTransactionResult, executor tunPlanExecutor) error {
	if executor == nil {
		return errors.New("missing TUN executor")
	}
	tx, _, err := result.Store.Load(result.TransactionID)
	if err != nil {
		return err
	}
	steps, err := executor.Apply(ctx, result.Plan)
	if err != nil {
		partialPlan := rollbackPlanFromAppliedSteps(result.Plan, steps)
		return newTunNetworkMutationError(result.Store, tx, partialPlan, steps, "network-apply", fmt.Errorf("apply TUN plan: %w", err))
	}
	appliedPlan := rollbackPlanFromAppliedSteps(result.Plan, steps)
	tx.AppliedSteps = appliedStepsFromExecutor(steps, transactionNow(result.Store))
	tx.Rollback = mergeRollbackMetadata(tx.Rollback, rollbackMetadataFromTunPlan(appliedPlan))
	if _, err := result.Store.Save(tx); err != nil {
		return newTunNetworkMutationError(result.Store, tx, appliedPlan, steps, "network-apply", fmt.Errorf("record applied TUN plan: %w", err))
	}
	if _, _, err := result.Store.Transition(tx.ID, txstate.TransactionApplied); err != nil {
		return newTunNetworkMutationError(result.Store, tx, appliedPlan, steps, "network-apply", err)
	}
	if _, _, err := result.Store.Transition(tx.ID, txstate.TransactionVerifying); err != nil {
		return newTunNetworkMutationError(result.Store, tx, appliedPlan, steps, "network-apply", err)
	}
	tx, _, err = result.Store.Load(tx.ID)
	if err != nil {
		return newTunNetworkMutationError(result.Store, tx, appliedPlan, steps, "network-apply", err)
	}
	if err := executor.Verify(ctx, result.Plan); err != nil {
		return newTunNetworkMutationError(result.Store, tx, appliedPlan, steps, "network-verify", fmt.Errorf("verify TUN plan: %w", err))
	}
	return nil
}

func newTunNetworkMutationError(store txstate.TransactionStore, tx txstate.Transaction, rollbackPlan planner.TunPlan, steps []netexecutor.Step, phase string, cause error) error {
	tx.AppliedSteps = appliedStepsFromExecutor(steps, transactionNow(store))
	tx.Rollback = mergeRollbackMetadata(tx.Rollback, rollbackMetadataFromTunPlan(rollbackPlan))
	if _, err := store.Save(tx); err != nil {
		cause = errors.Join(cause, fmt.Errorf("record failed TUN rollback ownership: %w", err))
	}
	return &tunNetworkMutationError{
		phase:        phase,
		store:        store,
		transaction:  tx,
		rollbackPlan: rollbackPlan,
		steps:        append([]netexecutor.Step(nil), steps...),
		cause:        cause,
	}
}

func commitTunTransaction(store txstate.TransactionStore, transactionID string) error {
	if _, _, err := store.Transition(transactionID, txstate.TransactionCommitted); err != nil {
		return fmt.Errorf("commit TUN transaction %s: %w", transactionID, err)
	}
	return nil
}

func saveGeneratedConfigRollbackMetadata(store txstate.TransactionStore, transactionID, runtimeConfigPath string, now time.Time) error {
	tx, _, err := store.Load(transactionID)
	if err != nil {
		return fmt.Errorf("load TUN transaction %s: %w", transactionID, err)
	}
	tx.DesiredPlan.Core = txstate.CorePlan{
		RuntimeConfigPath: runtimeConfigPath,
		ProcessLabel:      "xray",
		Owner:             txstate.TransactionOwner,
	}
	if !hasGeneratedConfigRollback(tx.Rollback, runtimeConfigPath) {
		tx.Rollback.GeneratedConfigs = append(tx.Rollback.GeneratedConfigs, txstate.GeneratedConfigRollback{Path: runtimeConfigPath, Owner: txstate.TransactionOwner})
	}
	tx.Health = txstate.HealthResult{Status: "core-preflight-planned", CheckedAt: now.UTC(), Message: "Xray generated config rollback metadata recorded before preflight writes the config"}
	_, err = store.Save(tx)
	return err
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
	if !hasGeneratedConfigRollback(tx.Rollback, runtimeConfigPath) {
		tx.Rollback.GeneratedConfigs = append(tx.Rollback.GeneratedConfigs, txstate.GeneratedConfigRollback{Path: runtimeConfigPath, Owner: txstate.TransactionOwner})
	}
	if pid > 0 {
		tx.Rollback.ChildProcesses = append(tx.Rollback.ChildProcesses, txstate.ChildProcessRollback{PID: pid, Label: "xray", ConfigRef: runtimeConfigPath, Owner: txstate.TransactionOwner})
	}
	tx.Health = txstate.HealthResult{Status: "core-started", CheckedAt: now.UTC(), Message: "Xray process stayed alive during startup verification"}
	_, err = store.Save(tx)
	return err
}

func hasGeneratedConfigRollback(metadata txstate.RollbackMetadata, path string) bool {
	for _, cfg := range metadata.GeneratedConfigs {
		if cfg.Path == path {
			return true
		}
	}
	return false
}

func mergeRollbackMetadata(base txstate.RollbackMetadata, extra txstate.RollbackMetadata) txstate.RollbackMetadata {
	base.TUN = append(base.TUN, extra.TUN...)
	base.Routes = append(base.Routes, extra.Routes...)
	base.PolicyRules = append(base.PolicyRules, extra.PolicyRules...)
	base.DNS = append(base.DNS, extra.DNS...)
	base.NFTables = append(base.NFTables, extra.NFTables...)
	base.GeneratedConfigs = append(base.GeneratedConfigs, extra.GeneratedConfigs...)
	base.ChildProcesses = append(base.ChildProcesses, extra.ChildProcesses...)
	return base
}

func newTunTransactionID(now func() time.Time) string {
	return "tun-" + now().UTC().Format("20060102T150405.000000000Z")
}
