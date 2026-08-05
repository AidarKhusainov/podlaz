package executor

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

// RollbackResourceScoped preserves the normal canonical rollback when the
// transaction-bound link identity is proven. If the link identity is missing,
// mismatched, or otherwise unproven, it still converges resources whose exact
// rollback does not depend on the current podlaz0 name, then returns the identity
// error so callers preserve transaction, child, and generated-config evidence.
func (e DNSAwareTunExecutor) RollbackResourceScoped(ctx context.Context, plan planner.TunPlan) error {
	if err := e.Base.VerifyRollbackIdentity(ctx, plan); err != nil {
		return errors.Join(e.rollbackIndependentResources(ctx, plan), fmt.Errorf("link-scoped rollback identity was not proven: %w", err))
	}
	return e.Rollback(ctx, plan)
}

func (e DNSAwareTunExecutor) rollbackIndependentResources(ctx context.Context, plan planner.TunPlan) error {
	var errs []error
	if e.Firewall != nil && strings.TrimSpace(plan.Firewall.Table) != "" {
		if err := e.Firewall.Rollback(ctx, plan.Firewall); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(plan.PolicyRules) - 1; i >= 0; i-- {
		rule := plan.PolicyRules[i]
		if rule.Action != "add" {
			continue
		}
		if e.Base.PolicyRules == nil {
			errs = append(errs, errors.New("missing policy-rule executor"))
			continue
		}
		if err := e.Base.PolicyRules.Rollback(ctx, rule); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(plan.Routes) - 1; i >= 0; i-- {
		route := plan.Routes[i]
		if route.Action != "add" || !independentTunRouteRollback(route) {
			continue
		}
		if e.Base.Routes == nil {
			errs = append(errs, errors.New("missing route executor"))
			continue
		}
		if err := e.Base.Routes.Rollback(ctx, route); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func independentTunRouteRollback(route planner.TunRoutePlan) bool {
	if strings.TrimSpace(route.Table) != planner.MainRoutingTable {
		return false
	}
	if strings.TrimSpace(route.Interface) == "" || strings.TrimSpace(route.Gateway) == "" {
		return false
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(route.Destination))
	if err != nil {
		return false
	}
	return prefix.Addr().Is4() && prefix.Bits() == 32
}
