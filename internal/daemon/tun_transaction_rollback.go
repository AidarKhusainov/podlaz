package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func rollbackTunFailure(ctx context.Context, store txstate.TransactionStore, tx *txstate.Transaction, rollbackPlan planner.TunPlan, executor tunPlanExecutor, steps []netexecutor.Step, cause error) error {
	tx.AppliedSteps = appliedStepsFromExecutor(steps, transactionNow(store))
	tx.Rollback = rollbackMetadataFromTunPlan(rollbackPlan)
	_, _ = store.Save(*tx)
	if err := rollbackTunTransaction(ctx, store, tx, rollbackPlan, executor); err != nil {
		_, _ = txstate.MarkFailure(tx, err.Error(), transactionNow(store))
		_, _ = store.Save(*tx)
		return errors.Join(cause, fmt.Errorf("rollback TUN plan: %w", err))
	}
	return fmt.Errorf("%w; rolled back applied podlaz-owned TUN, route, policy-rule, DNS, and nftables state", cause)
}

func rollbackTunTransaction(ctx context.Context, store txstate.TransactionStore, tx *txstate.Transaction, plan planner.TunPlan, executor tunPlanExecutor) error {
	if executor == nil {
		return errors.New("missing TUN executor")
	}
	if tx.State == txstate.TransactionRolledBack {
		return nil
	}
	if tx.State != txstate.TransactionRollingBack {
		if _, err := txstate.Transition(tx, txstate.TransactionRollingBack, transactionNow(store)); err != nil {
			return err
		}
		if _, err := store.Save(*tx); err != nil {
			return err
		}
	}

	var rollbackErrs []error
	if err := stopRollbackChildProcesses(*tx); err != nil {
		rollbackErrs = append(rollbackErrs, err)
	}
	for _, cfg := range tx.Rollback.GeneratedConfigs {
		removeGeneratedConfig(cfg.Path)
	}
	if err := executor.Rollback(ctx, plan); err != nil {
		rollbackErrs = append(rollbackErrs, err)
	}
	if len(rollbackErrs) > 0 {
		return errors.Join(rollbackErrs...)
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

func rollbackPlanFromAppliedSteps(plan planner.TunPlan, steps []netexecutor.Step) planner.TunPlan {
	rollback := planner.TunPlan{Mode: plan.Mode, TunnelMode: plan.TunnelMode, ProfileID: plan.ProfileID, ProfileName: plan.ProfileName}
	for _, step := range steps {
		switch step.Kind {
		case "tun-device":
			if step.Target == plan.TunDevice.Name {
				rollback.TunDevice = plan.TunDevice
			}
		case "route":
			for _, route := range plan.Routes {
				if routeTarget(route) == step.Target {
					rollback.Routes = append(rollback.Routes, route)
				}
			}
		case "policy-rule":
			for _, rule := range plan.PolicyRules {
				if policyRuleTarget(rule) == step.Target {
					rollback.PolicyRules = append(rollback.PolicyRules, rule)
				}
			}
		case "dns":
			if step.Target == plan.DNS.TargetLink {
				rollback.DNS = plan.DNS
			}
		case "nftables":
			if step.Target == firewallTarget(plan.Firewall) {
				rollback.Firewall = plan.Firewall
			}
		}
	}
	return rollback
}
