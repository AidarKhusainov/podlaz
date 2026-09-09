package executor

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestTunRollbackStopsAtFirstPolicyRuleFailure(t *testing.T) {
	recorder := &callRecorder{}
	blocker := errors.New("synthetic policy-rule rollback blocker")
	exec := TunExecutor{
		TunDevice:   fakeTun{rec: recorder},
		TunAddress:  fakeTunAddress{rec: recorder},
		Routes:      fakeRoutes{rec: recorder},
		PolicyRules: rollbackFailRules{rec: recorder, failPriority: planner.TunRulePriority, err: blocker},
	}
	plan := executorPlanForTest()
	plan.TunAddress = rollbackIdentityAddressPlanForTest()

	err := exec.Rollback(context.Background(), plan)
	if !errors.Is(err, blocker) {
		t.Fatalf("rollback error=%v, want %v", err, blocker)
	}
	want := []string{"rule:rollback:10000:from all"}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("policy-rule blocker must stop later rollback phases:\nwant %#v\n got %#v", want, recorder.calls)
	}
}

func TestTunRollbackStopsAtFirstRouteFailureBeforeAddress(t *testing.T) {
	recorder := &callRecorder{}
	blocker := errors.New("synthetic route rollback blocker")
	exec := TunExecutor{
		TunDevice:   fakeTun{rec: recorder},
		TunAddress:  fakeTunAddress{rec: recorder},
		Routes:      rollbackFailRoutes{rec: recorder, failTarget: "main:203.0.113.10/32", err: blocker},
		PolicyRules: fakeRules{rec: recorder},
	}
	plan := executorPlanForTest()
	plan.TunAddress = rollbackIdentityAddressPlanForTest()

	err := exec.Rollback(context.Background(), plan)
	if !errors.Is(err, blocker) {
		t.Fatalf("rollback error=%v, want %v", err, blocker)
	}
	want := []string{
		"rule:rollback:10000:from all",
		"rule:rollback:9999:to 203.0.113.10/32",
		"route:rollback:main:203.0.113.10/32",
	}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("route blocker must stop later routes/address:\nwant %#v\n got %#v", want, recorder.calls)
	}
}

type rollbackFailRules struct {
	rec          *callRecorder
	failPriority int
	err          error
}

func (f rollbackFailRules) Add(context.Context, planner.TunPolicyRulePlan) (Step, error) {
	return Step{}, errors.New("unexpected rule add")
}

func (f rollbackFailRules) Verify(context.Context, planner.TunPolicyRulePlan) error {
	return errors.New("unexpected rule verify")
}

func (f rollbackFailRules) Rollback(_ context.Context, plan planner.TunPolicyRulePlan) error {
	f.rec.calls = append(f.rec.calls, "rule:rollback:"+ruleCallTarget(plan))
	if plan.Priority == f.failPriority {
		return f.err
	}
	return nil
}

type rollbackFailRoutes struct {
	rec        *callRecorder
	failTarget string
	err        error
}

func (f rollbackFailRoutes) Add(context.Context, planner.TunRoutePlan) (Step, error) {
	return Step{}, errors.New("unexpected route add")
}

func (f rollbackFailRoutes) Verify(context.Context, planner.TunRoutePlan) error {
	return errors.New("unexpected route verify")
}

func (f rollbackFailRoutes) Rollback(_ context.Context, plan planner.TunRoutePlan) error {
	target := plan.Table + ":" + plan.Destination
	f.rec.calls = append(f.rec.calls, "route:rollback:"+target)
	if target == f.failTarget {
		return f.err
	}
	return nil
}
