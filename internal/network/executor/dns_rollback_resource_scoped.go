package executor

import (
	"context"
	"errors"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

// RollbackResourceScoped preserves the pre-mutation identity gate for
// link-scoped resources while still converging transaction-owned resources that
// do not depend on the current podlaz0 identity. It intentionally returns the
// identity error after independent cleanup so lifecycle callers preserve
// transaction/config/child evidence until the link-scoped subset converges.
func (e DNSAwareTunExecutor) RollbackResourceScoped(ctx context.Context, plan planner.TunPlan) error {
	if err := e.Base.VerifyRollbackIdentity(ctx, plan); err != nil {
		var errs []error
		if e.Firewall != nil && strings.TrimSpace(plan.Firewall.Table) != "" {
			if rollbackErr := e.Firewall.Rollback(ctx, plan.Firewall); rollbackErr != nil {
				errs = append(errs, rollbackErr)
			}
		}
		if rollbackErr := e.Base.RollbackIndependent(ctx, plan); rollbackErr != nil {
			errs = append(errs, rollbackErr)
		}
		errs = append(errs, err)
		return errors.Join(errs...)
	}
	return e.Rollback(ctx, plan)
}

// RollbackIndependent removes only exact resources whose mutation does not rely
// on a current podlaz0 name/ifindex/kind proof. It never mutates DNS, TUN
// address, or TUN link state.
func (e TunExecutor) RollbackIndependent(ctx context.Context, plan planner.TunPlan) error {
	if err := e.validatePlan(plan); err != nil {
		return err
	}
	var errs []error
	for i := len(plan.PolicyRules) - 1; i >= 0; i-- {
		rule := plan.PolicyRules[i]
		if rule.Action != "add" {
			continue
		}
		if err := e.PolicyRules.Rollback(ctx, rule); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(plan.Routes) - 1; i >= 0; i-- {
		route := plan.Routes[i]
		if !independentRollbackRoute(route) {
			continue
		}
		if err := e.Routes.Rollback(ctx, route); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func independentRollbackRoute(route planner.TunRoutePlan) bool {
	if route.Action != "add" {
		return false
	}
	if strings.TrimSpace(route.Table) != planner.MainRoutingTable {
		return false
	}
	if strings.TrimSpace(route.Interface) == "" || route.Interface == "podlaz0" {
		return false
	}
	return strings.TrimSpace(route.Destination) != "" && strings.TrimSpace(route.Gateway) != ""
}
