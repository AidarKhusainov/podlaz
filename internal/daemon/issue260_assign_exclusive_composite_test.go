package daemon

import (
	"context"
	"reflect"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestXrayOwnedAllocatedTunPlanExecutesExclusiveAddressThroughCompositeLifecycle(t *testing.T) {
	basePlan, err := planner.PlanTunForSession(
		profileFromSnapshot(connectRequestForTest().Profile),
		netsnapshot.FakeResolvedDesktop(),
		planner.TunOptions{},
	)
	if err != nil {
		t.Fatalf("plan allocated TUN session: %v", err)
	}
	plan := xrayOwnedTunPlan(basePlan)
	if plan.TunAddress.Action != planner.TunAddressActionAssignExclusive {
		t.Fatalf("xray-owned allocated plan address action = %q, want %q", plan.TunAddress.Action, planner.TunAddressActionAssignExclusive)
	}

	recorder := &assignExclusiveCompositeRecorder{}
	executor := netexecutor.TunExecutor{
		TunDevice:   assignExclusiveTunDevice{},
		TunAddress:  assignExclusiveTunAddress{recorder: recorder},
		Routes:      assignExclusiveRoutes{},
		PolicyRules: assignExclusivePolicyRules{},
	}

	steps, err := executor.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply xray-owned allocated plan: %v", err)
	}
	if len(steps) == 0 || steps[0].Kind != "tun-address" {
		t.Fatalf("composite apply must retain TUN-address ownership step, got %#v", steps)
	}
	if err := executor.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify xray-owned allocated plan: %v", err)
	}
	if err := executor.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback xray-owned allocated plan: %v", err)
	}

	wantAddressCalls := []string{"apply", "verify", "rollback"}
	if !reflect.DeepEqual(recorder.addressCalls, wantAddressCalls) {
		t.Fatalf("exclusive TUN address composite lifecycle calls = %#v, want %#v", recorder.addressCalls, wantAddressCalls)
	}
}

type assignExclusiveCompositeRecorder struct {
	addressCalls []string
}

type assignExclusiveTunDevice struct{}

func (assignExclusiveTunDevice) Create(context.Context, planner.TunDevicePlan) (netexecutor.Step, error) {
	return netexecutor.Step{}, nil
}

func (assignExclusiveTunDevice) Verify(context.Context, planner.TunDevicePlan) error {
	return nil
}

func (assignExclusiveTunDevice) Rollback(context.Context, planner.TunDevicePlan) error {
	return nil
}

type assignExclusiveTunAddress struct {
	recorder *assignExclusiveCompositeRecorder
}

func (assignExclusiveTunAddress) Bind(_ context.Context, plan planner.TunAddressPlan, _ netexecutor.TunLinkCreationProof) (planner.TunAddressPlan, error) {
	return plan, nil
}

func (f assignExclusiveTunAddress) Apply(_ context.Context, plan planner.TunAddressPlan) (netexecutor.Step, error) {
	f.recorder.addressCalls = append(f.recorder.addressCalls, "apply")
	return netexecutor.Step{Kind: "tun-address", Target: plan.Interface + ":" + plan.CIDR, Owner: netexecutor.OwnerTunAddress}, nil
}

func (f assignExclusiveTunAddress) Verify(context.Context, planner.TunAddressPlan) error {
	f.recorder.addressCalls = append(f.recorder.addressCalls, "verify")
	return nil
}

func (f assignExclusiveTunAddress) Rollback(context.Context, planner.TunAddressPlan) error {
	f.recorder.addressCalls = append(f.recorder.addressCalls, "rollback")
	return nil
}

type assignExclusiveRoutes struct{}

func (assignExclusiveRoutes) Add(_ context.Context, plan planner.TunRoutePlan) (netexecutor.Step, error) {
	return netexecutor.Step{Kind: "route", Target: plan.Table + " " + plan.Destination, Owner: netexecutor.OwnerRoute}, nil
}

func (assignExclusiveRoutes) Verify(context.Context, planner.TunRoutePlan) error {
	return nil
}

func (assignExclusiveRoutes) Rollback(context.Context, planner.TunRoutePlan) error {
	return nil
}

type assignExclusivePolicyRules struct{}

func (assignExclusivePolicyRules) Add(_ context.Context, plan planner.TunPolicyRulePlan) (netexecutor.Step, error) {
	return netexecutor.Step{Kind: "policy-rule", Owner: netexecutor.OwnerPolicyRule}, nil
}

func (assignExclusivePolicyRules) Verify(context.Context, planner.TunPolicyRulePlan) error {
	return nil
}

func (assignExclusivePolicyRules) Rollback(context.Context, planner.TunPolicyRulePlan) error {
	return nil
}
