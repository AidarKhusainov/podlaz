package daemon

import (
	"context"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTunTransactionRecordsRollbackOnlyForAppliedSteps(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &appliedOnlyTunExecutor{steps: []netexecutor.Step{
		{Kind: "tun-device", Target: "podlaz0", Owner: netexecutor.OwnerTunDevice},
		{Kind: "route", Target: "podlaz default", Owner: netexecutor.OwnerRoute},
	}}
	result, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanForTest(), executor, fixedClock())
	if err != nil {
		t.Fatalf("run TUN transaction failed: %v", err)
	}
	tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(result.TransactionID)
	if err != nil {
		t.Fatalf("load transaction: %v", err)
	}
	if len(tx.Rollback.Routes) != 1 || tx.Rollback.Routes[0].CIDR != "default" {
		t.Fatalf("unexpected route rollback metadata: %#v", tx.Rollback.Routes)
	}
	if len(tx.Rollback.PolicyRules) != 0 {
		t.Fatalf("unexpected policy rule rollback metadata: %#v", tx.Rollback.PolicyRules)
	}
	if len(tx.Rollback.TUN) != 1 {
		t.Fatalf("unexpected TUN rollback metadata: %#v", tx.Rollback.TUN)
	}
}

func TestDesiredPlanRecordsXrayOwnerForVerifiedTunLink(t *testing.T) {
	plan := transactionPlanForTest()
	plan.TunDevice.Action = "verify"
	desired := desiredPlanFromTunPlan(plan)
	if desired.TUN.Owner != xrayTunInboundOwner {
		t.Fatalf("expected Xray TUN owner for verified link, got %q", desired.TUN.Owner)
	}
	rollback := rollbackMetadataFromTunPlan(plan)
	if len(rollback.TUN) != 0 {
		t.Fatalf("verified Xray-owned link must not produce podlaz TUN rollback metadata: %#v", rollback.TUN)
	}
}

func TestLegacyTunAddActionStillRecordsOwnedRollbackMetadata(t *testing.T) {
	plan := transactionPlanForTest()
	plan.TunDevice.Action = "add"

	desired := desiredPlanFromTunPlan(plan)
	if desired.TUN.Owner != netexecutor.OwnerTunDevice {
		t.Fatalf("expected legacy add action to remain podlaz-owned, got %q", desired.TUN.Owner)
	}
	rollback := rollbackMetadataFromTunPlan(plan)
	if len(rollback.TUN) != 1 || rollback.TUN[0].InterfaceName != "podlaz0" || rollback.TUN[0].Owner != netexecutor.OwnerTunDevice {
		t.Fatalf("expected legacy add action TUN rollback metadata, got %#v", rollback.TUN)
	}
}

type appliedOnlyTunExecutor struct {
	steps []netexecutor.Step
}

func (e *appliedOnlyTunExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return e.steps, nil
}

func (e *appliedOnlyTunExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (e *appliedOnlyTunExecutor) Rollback(context.Context, planner.TunPlan) error { return nil }
