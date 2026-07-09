package daemon

import (
	"context"
	"errors"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestTunTransactionRollbackSkipsPreExistingServerBypassRouteAndRule(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &preExistingServerBypassExecutor{verifyErr: errors.New("verify failed after pre-existing bypass skip")}
	_, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanWithServerBypassForTest(), executor, fixedClock())
	if err == nil {
		t.Fatal("expected verify failure")
	}
	if executor.rollbackPlan == nil {
		t.Fatal("expected rollback plan to be captured")
	}
	for _, route := range executor.rollbackPlan.Routes {
		if route.Destination == "203.0.113.10/32" && route.Table == planner.MainRoutingTable {
			t.Fatalf("rollback plan must not delete pre-existing main-table server bypass route: %#v", executor.rollbackPlan.Routes)
		}
	}
	for _, rule := range executor.rollbackPlan.PolicyRules {
		if rule.Priority == planner.ServerRulePriority && rule.Table == planner.MainRoutingTable {
			t.Fatalf("rollback plan must not delete pre-existing server bypass policy rule: %#v", executor.rollbackPlan.PolicyRules)
		}
	}
}

type preExistingServerBypassExecutor struct {
	verifyErr    error
	rollbackPlan *planner.TunPlan
}

func (e *preExistingServerBypassExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return []netexecutor.Step{
		{Kind: "tun-device", Target: "podlaz0", Owner: netexecutor.OwnerTunDevice},
		{Kind: "route", Target: planner.TunRoutingTable + " default", Owner: netexecutor.OwnerRoute},
		{Kind: "policy-rule", Target: "priority 10000 from all lookup podlaz", Owner: netexecutor.OwnerPolicyRule},
	}, nil
}

func (e *preExistingServerBypassExecutor) Verify(context.Context, planner.TunPlan) error {
	return e.verifyErr
}

func (e *preExistingServerBypassExecutor) Rollback(_ context.Context, plan planner.TunPlan) error {
	cloned := plan
	e.rollbackPlan = &cloned
	return nil
}
