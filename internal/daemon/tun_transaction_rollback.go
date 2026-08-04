package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type tunRollbackChildStopper func(txstate.Transaction) error

func rollbackTunFailure(ctx context.Context, store txstate.TransactionStore, tx *txstate.Transaction, rollbackPlan planner.TunPlan, executor tunPlanExecutor, steps []netexecutor.Step, cause error) error {
	if err := prepareTunFailureRollback(store, tx, rollbackPlan, steps); err != nil {
		cause = errors.Join(cause, fmt.Errorf("record failed TUN rollback ownership: %w", err))
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tunRollbackCleanupTimeout)
	defer cancel()
	if err := rollbackPreparedTunFailure(cleanupCtx, store, tx, rollbackPlan, executor); err != nil {
		return errors.Join(cause, fmt.Errorf("rollback TUN plan: %w", err))
	}
	return fmt.Errorf("%w; rolled back applied podlaz-owned TUN, route, policy-rule, DNS, and nftables state", cause)
}

func prepareTunFailureRollback(store txstate.TransactionStore, tx *txstate.Transaction, rollbackPlan planner.TunPlan, steps []netexecutor.Step) error {
	if tx == nil {
		return errors.New("missing TUN transaction")
	}
	tx.AppliedSteps = appliedStepsFromExecutor(steps, transactionNow(store))
	tx.Rollback = mergeRollbackMetadata(tx.Rollback, rollbackMetadataFromTunPlan(rollbackPlan))
	_, err := store.Save(*tx)
	return err
}

func rollbackPreparedTunFailure(ctx context.Context, store txstate.TransactionStore, tx *txstate.Transaction, rollbackPlan planner.TunPlan, executor tunPlanExecutor) error {
	return rollbackPreparedTunFailureWithChildStopper(ctx, store, tx, rollbackPlan, executor, stopRollbackChildProcesses)
}

func rollbackPreparedTunFailureWithChildStopper(ctx context.Context, store txstate.TransactionStore, tx *txstate.Transaction, rollbackPlan planner.TunPlan, executor tunPlanExecutor, stopChildren tunRollbackChildStopper) error {
	if tx == nil {
		return errors.New("missing TUN transaction")
	}
	if err := rollbackTunTransactionWithChildStopper(ctx, store, tx, rollbackPlan, executor, stopChildren); err != nil {
		_, _ = txstate.MarkFailure(tx, err.Error(), transactionNow(store))
		_, _ = store.Save(*tx)
		return err
	}
	if err := removeTransactionFile(store, tx.ID); err != nil {
		return fmt.Errorf("rolled-back transaction file cleanup failed: %w", err)
	}
	return nil
}

func rollbackTunTransaction(ctx context.Context, store txstate.TransactionStore, tx *txstate.Transaction, plan planner.TunPlan, executor tunPlanExecutor) error {
	return rollbackTunTransactionWithChildStopper(ctx, store, tx, plan, executor, stopRollbackChildProcesses)
}

func rollbackTunTransactionWithChildStopper(ctx context.Context, store txstate.TransactionStore, tx *txstate.Transaction, plan planner.TunPlan, executor tunPlanExecutor, stopChildren tunRollbackChildStopper) error {
	if tx.State == txstate.TransactionRolledBack {
		return nil
	}
	projection := recovery.ProjectRollbackMetadata(*tx)
	if projection.Incomplete && !rollbackPlanHasOwnedResource(plan) {
		return fmt.Errorf("TUN rollback authority incomplete: %s", strings.Join(projection.Reasons, "; "))
	}
	if err := beginTunRollback(store, tx); err != nil {
		return err
	}
	if stopChildren == nil {
		stopChildren = stopRollbackChildProcesses
	}

	if err := rollbackTunHostState(ctx, plan, executor); err != nil {
		// The Xray-owned link carries the exact address identity needed for a
		// safe retry. Keep the tracked child and generated config intact until
		// every daemon-owned host resource is proven rolled back.
		return err
	}
	if projection.Incomplete {
		return fmt.Errorf("TUN rollback authority incomplete after partial cleanup: %s", strings.Join(projection.Reasons, "; "))
	}
	if err := stopChildren(*tx); err != nil {
		return err
	}

	// Runtime configuration is process identity material. It may be removed only
	// after host rollback succeeded and the owned Xray process is proven absent.
	if err := removeRollbackGeneratedConfigs(*tx); err != nil {
		return err
	}
	return finishTunRollback(store, tx)
}

func beginTunRollback(store txstate.TransactionStore, tx *txstate.Transaction) error {
	if tx.State == txstate.TransactionRolledBack {
		return nil
	}
	if tx.State == txstate.TransactionRollingBack {
		return nil
	}
	if _, err := txstate.Transition(tx, txstate.TransactionRollingBack, transactionNow(store)); err != nil {
		return err
	}
	_, err := store.Save(*tx)
	return err
}

func rollbackTunHostState(ctx context.Context, plan planner.TunPlan, executor tunPlanExecutor) error {
	if executor == nil {
		return errors.New("missing TUN executor")
	}
	return executor.Rollback(ctx, plan)
}

func removeRollbackGeneratedConfigs(tx txstate.Transaction) error {
	var errs []error
	for _, cfg := range tx.Rollback.GeneratedConfigs {
		if err := removeGeneratedConfig(cfg.Path); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func verifyRollbackGeneratedConfigsRemoved(tx txstate.Transaction) error {
	parents := make(map[string]struct{})
	var errs []error
	for _, cfg := range tx.Rollback.GeneratedConfigs {
		if cfg.Path == "" {
			errs = append(errs, errors.New("generated runtime config rollback path is empty"))
			continue
		}
		path := filepath.Clean(cfg.Path)
		parents[filepath.Dir(path)] = struct{}{}
		if _, err := os.Lstat(path); err == nil {
			errs = append(errs, fmt.Errorf("generated runtime config still exists: %s", path))
		} else if !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("verify generated runtime config removal %s: %w", path, err))
		}
	}
	for parent := range parents {
		entries, err := os.ReadDir(parent)
		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			errs = append(errs, fmt.Errorf("verify generated runtime config directory removal %s: %w", parent, err))
		case len(entries) > 0:
			errs = append(errs, fmt.Errorf("generated runtime config directory is not empty: %s", parent))
		default:
			errs = append(errs, fmt.Errorf("generated runtime config directory still exists: %s", parent))
		}
	}
	return errors.Join(errs...)
}

func finishTunRollback(store txstate.TransactionStore, tx *txstate.Transaction) error {
	if tx.State == txstate.TransactionRolledBack {
		return nil
	}
	if err := verifyRollbackGeneratedConfigsRemoved(*tx); err != nil {
		return err
	}
	if _, err := txstate.Transition(tx, txstate.TransactionRolledBack, transactionNow(store)); err != nil {
		return err
	}
	_, err := store.Save(*tx)
	return err
}

func removeTransactionFile(store txstate.TransactionStore, transactionID string) error {
	path, err := store.Path(transactionID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func rollbackPlanFromPersistedTransaction(plan planner.TunPlan, tx txstate.Transaction) planner.TunPlan {
	steps := make([]netexecutor.Step, 0, len(tx.AppliedSteps))
	for _, step := range tx.AppliedSteps {
		steps = append(steps, netexecutor.Step{
			Kind:        step.Kind,
			Target:      step.Target,
			Description: step.Description,
			Owner:       step.Owner,
		})
	}
	return rollbackPlanFromAppliedSteps(plan, steps)
}

func rollbackPlanFromAppliedSteps(plan planner.TunPlan, steps []netexecutor.Step) planner.TunPlan {
	rollback := planner.TunPlan{Mode: plan.Mode, TunnelMode: plan.TunnelMode, ProfileID: plan.ProfileID, ProfileName: plan.ProfileName}
	for _, step := range steps {
		switch step.Kind {
		case "tun-device":
			if step.Owner == netexecutor.OwnerTunDevice && step.Target == plan.TunDevice.Name {
				rollback.TunDevice = plan.TunDevice
			}
		case "tun-address":
			if step.Owner == netexecutor.OwnerTunAddress && step.Target == tunAddressTarget(plan.TunAddress) {
				rollback.TunAddress = plan.TunAddress
			}
		case "route":
			if step.Owner != netexecutor.OwnerRoute {
				continue
			}
			for _, route := range plan.Routes {
				if routeTarget(route) == step.Target {
					rollback.Routes = append(rollback.Routes, route)
				}
			}
		case "policy-rule":
			if step.Owner != netexecutor.OwnerPolicyRule {
				continue
			}
			for _, rule := range plan.PolicyRules {
				if policyRuleTarget(rule) == step.Target {
					rollback.PolicyRules = append(rollback.PolicyRules, rule)
				}
			}
		case "dns":
			if step.Owner == netexecutor.OwnerDNS && step.Target == plan.DNS.TargetLink {
				rollback.DNS = plan.DNS
			}
		case "nftables":
			if step.Owner == netexecutor.OwnerFirewall && step.Target == firewallTarget(plan.Firewall) {
				rollback.Firewall = plan.Firewall
			}
		}
	}
	return rollback
}

func filterOwnedAppliedSteps(plan planner.TunPlan, steps []netexecutor.Step) []netexecutor.Step {
	owned := make([]netexecutor.Step, 0, len(steps))
	for _, step := range steps {
		rollback := rollbackPlanFromAppliedSteps(plan, []netexecutor.Step{step})
		if rollbackPlanHasOwnedResource(rollback) {
			owned = append(owned, step)
		}
	}
	return owned
}

func rollbackPlanHasOwnedResource(plan planner.TunPlan) bool {
	return plan.TunDevice.Name != "" || plan.TunAddress.CIDR != "" || len(plan.Routes) > 0 || len(plan.PolicyRules) > 0 || plan.DNS.TargetLink != "" || plan.Firewall.Table != ""
}
