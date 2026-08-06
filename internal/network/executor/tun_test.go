package executor

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestTunExecutorApplyVerifyAndRollbackOrder(t *testing.T) {
	recorder := &callRecorder{}
	exec := TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}}
	plan := executorPlanForTest()

	steps, err := exec.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if len(steps) != 4 {
		t.Fatalf("expected 4 applied route/rule steps, got %#v", steps)
	}
	for _, step := range steps {
		if step.Kind == "tun-device" {
			t.Fatalf("Xray-owned TUN link must not produce daemon ownership step: %#v", steps)
		}
	}
	if err := exec.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	want := []string{
		"tun:verify:podlaz0",
		"route:add:podlaz:default",
		"route:add:main:203.0.113.10/32",
		"rule:add:9999:to 203.0.113.10/32",
		"rule:add:10000:from all",
		"tun:verify:podlaz0",
		"route:verify:podlaz:default",
		"route:verify:main:203.0.113.10/32",
		"rule:verify:9999:to 203.0.113.10/32",
		"rule:verify:10000:from all",
		"rule:rollback:10000:from all",
		"rule:rollback:9999:to 203.0.113.10/32",
		"route:rollback:main:203.0.113.10/32",
		"route:rollback:podlaz:default",
	}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("unexpected calls:\nwant %#v\n got %#v", want, recorder.calls)
	}
}

func TestTunExecutorRollbackRejectsLegacyTunAddAction(t *testing.T) {
	recorder := &callRecorder{}
	exec := TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}}
	plan := executorPlanForTest()
	plan.TunDevice.Action = "add"
	plan.Routes = nil
	plan.PolicyRules = nil

	if err := exec.Rollback(context.Background(), plan); err == nil {
		t.Fatal("expected legacy add rollback to be rejected without typed creation proof")
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("legacy add rollback must not mutate TUN by name, got calls %#v", recorder.calls)
	}
}

func TestTunExecutorApplySkipsUnmutatedSteps(t *testing.T) {
	recorder := &callRecorder{}
	exec := TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder, skipTarget: "main:203.0.113.10/32"}, PolicyRules: fakeRules{rec: recorder}}
	plan := executorPlanForTest()

	steps, err := exec.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	for _, step := range steps {
		if step.Kind == "route" && step.Target == "main 203.0.113.10/32" {
			t.Fatalf("pre-existing route should not be recorded as applied: %#v", steps)
		}
		if step.Kind == "tun-device" {
			t.Fatalf("Xray-owned TUN link should not be recorded as applied: %#v", steps)
		}
	}
	if len(steps) != 3 {
		t.Fatalf("expected managed route and policy rules only, got %#v", steps)
	}
}

func TestTunExecutorApplyFailureLeavesRollbackablePartialState(t *testing.T) {
	recorder := &callRecorder{}
	exec := TunExecutor{
		TunDevice:   fakeTun{rec: recorder},
		Routes:      fakeRoutes{rec: recorder, failTarget: "main:203.0.113.10/32"},
		PolicyRules: fakeRules{rec: recorder},
	}
	plan := executorPlanForTest()

	steps, err := exec.Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("expected apply failure")
	}
	if len(steps) != 1 || steps[0].Kind == "tun-device" {
		t.Fatalf("expected only first route as applied partial state, got %#v", steps)
	}
}

func TestTunExecutorAppliesAddressBeforeRoutesAndRollsItBackBeforeLink(t *testing.T) {
	recorder := &callRecorder{}
	exec := TunExecutor{
		TunDevice:   fakeTun{rec: recorder},
		TunAddress:  fakeTunAddress{rec: recorder},
		Routes:      fakeRoutes{rec: recorder},
		PolicyRules: fakeRules{rec: recorder},
	}
	plan := executorPlanForTest()
	plan.TunAddress = boundAddressPlanForTest()

	steps, err := exec.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply plan with TUN address: %v", err)
	}
	if len(steps) != 5 || steps[0].Kind != "tun-address" {
		t.Fatalf("expected address plus route/rule ownership, got %#v", steps)
	}
	if err := exec.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify plan with TUN address: %v", err)
	}
	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback plan with TUN address: %v", err)
	}

	want := []string{
		"tun:verify:podlaz0",
		"address:apply:podlaz0:" + planner.DefaultTunIPv4CIDR,
		"route:add:podlaz:default",
		"route:add:main:203.0.113.10/32",
		"rule:add:9999:to 203.0.113.10/32",
		"rule:add:10000:from all",
		"tun:verify:podlaz0",
		"address:verify:podlaz0:" + planner.DefaultTunIPv4CIDR,
		"route:verify:podlaz:default",
		"route:verify:main:203.0.113.10/32",
		"rule:verify:9999:to 203.0.113.10/32",
		"rule:verify:10000:from all",
		"rule:rollback:10000:from all",
		"rule:rollback:9999:to 203.0.113.10/32",
		"route:rollback:main:203.0.113.10/32",
		"route:rollback:podlaz:default",
		"address:rollback:podlaz0:" + planner.DefaultTunIPv4CIDR,
	}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("unexpected address-aware order:\nwant %#v\n got %#v", want, recorder.calls)
	}
}

func TestTunExecutorApplyWithStepSinkPersistsEachMutationBeforeNext(t *testing.T) {
	recorder := &callRecorder{}
	exec := TunExecutor{
		TunDevice:   fakeTun{rec: recorder},
		TunAddress:  fakeTunAddress{rec: recorder},
		Routes:      fakeRoutes{rec: recorder},
		PolicyRules: fakeRules{rec: recorder},
	}
	plan := executorPlanForTest()
	plan.TunAddress = boundAddressPlanForTest()

	_, err := exec.ApplyWithStepSink(context.Background(), plan, func(step Step) error {
		recorder.calls = append(recorder.calls, "persist:"+step.Kind)
		return nil
	})
	if err != nil {
		t.Fatalf("apply with step sink: %v", err)
	}

	want := []string{
		"tun:verify:podlaz0",
		"address:apply:podlaz0:" + planner.DefaultTunIPv4CIDR,
		"persist:tun-address",
		"route:add:podlaz:default",
		"persist:route",
		"route:add:main:203.0.113.10/32",
		"persist:route",
		"rule:add:9999:to 203.0.113.10/32",
		"persist:policy-rule",
		"rule:add:10000:from all",
		"persist:policy-rule",
	}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("ownership must be persisted before the next mutation:\nwant %#v\n got %#v", want, recorder.calls)
	}
}

func TestTunExecutorApplyWithStepSinkStopsBeforeNextMutationWhenPersistenceFails(t *testing.T) {
	recorder := &callRecorder{}
	exec := TunExecutor{
		TunDevice:   fakeTun{rec: recorder},
		TunAddress:  fakeTunAddress{rec: recorder},
		Routes:      fakeRoutes{rec: recorder},
		PolicyRules: fakeRules{rec: recorder},
	}
	plan := executorPlanForTest()
	plan.TunAddress = boundAddressPlanForTest()
	persistErr := errors.New("persist address ownership")

	steps, err := exec.ApplyWithStepSink(context.Background(), plan, func(step Step) error {
		recorder.calls = append(recorder.calls, "persist:"+step.Kind)
		return persistErr
	})
	if !errors.Is(err, persistErr) {
		t.Fatalf("expected persistence failure, got %v", err)
	}
	if len(steps) != 1 || steps[0].Kind != "tun-address" {
		t.Fatalf("mutated address must remain rollbackable, got %#v", steps)
	}
	want := []string{
		"tun:verify:podlaz0",
		"address:apply:podlaz0:" + planner.DefaultTunIPv4CIDR,
		"persist:tun-address",
	}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("executor must stop before route mutation:\nwant %#v\n got %#v", want, recorder.calls)
	}
}
