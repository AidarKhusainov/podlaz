package executor

import (
	"context"
	"reflect"
	"testing"
)

func TestTunExecutorApplyVerifyAndRollbackOrder(t *testing.T) {
	recorder := &callRecorder{}
	exec := TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}}
	plan := executorPlanForTest()

	steps, err := exec.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if len(steps) != 5 {
		t.Fatalf("expected 5 applied steps, got %#v", steps)
	}
	if err := exec.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	want := []string{
		"tun:create:podlaz0",
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
		"tun:rollback:podlaz0",
	}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("unexpected calls:\nwant %#v\n got %#v", want, recorder.calls)
	}
}

func TestTunExecutorRollbackTreatsLegacyTunAddActionAsOwnedLink(t *testing.T) {
	recorder := &callRecorder{}
	exec := TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}}
	plan := executorPlanForTest()
	plan.TunDevice.Action = "add"
	plan.Routes = nil
	plan.PolicyRules = nil

	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback legacy add action: %v", err)
	}
	want := []string{"tun:rollback:podlaz0"}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("unexpected calls:\nwant %#v\n got %#v", want, recorder.calls)
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
	}
	if len(steps) != 4 {
		t.Fatalf("expected TUN, managed route, and policy rules only, got %#v", steps)
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
	if len(steps) != 2 {
		t.Fatalf("expected TUN and first route as applied partial state, got %#v", steps)
	}
}
