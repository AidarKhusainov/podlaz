package executor

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
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
		"tun:create:tunwarden0",
		"route:add:tunwarden:default",
		"route:add:main:203.0.113.10/32",
		"rule:add:51819:to 203.0.113.10/32",
		"rule:add:51820:from all",
		"tun:verify:tunwarden0",
		"route:verify:tunwarden:default",
		"route:verify:main:203.0.113.10/32",
		"rule:verify:51819:to 203.0.113.10/32",
		"rule:verify:51820:from all",
		"rule:rollback:51820:from all",
		"rule:rollback:51819:to 203.0.113.10/32",
		"route:rollback:main:203.0.113.10/32",
		"route:rollback:tunwarden:default",
		"tun:rollback:tunwarden0",
	}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("unexpected calls:\nwant %#v\n got %#v", want, recorder.calls)
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

func TestIPRouteAndRuleMappingUsesAddAndTunWardenTableID(t *testing.T) {
	runner := &recordingRunner{}
	routes := IPRouteExecutor{Runner: runner}
	rules := IPPolicyRuleExecutor{Runner: runner}
	plan := executorPlanForTest()

	if _, err := routes.Add(context.Background(), plan.Routes[0]); err != nil {
		t.Fatalf("add default route: %v", err)
	}
	if _, err := routes.Add(context.Background(), plan.Routes[1]); err != nil {
		t.Fatalf("add server bypass: %v", err)
	}
	if _, err := rules.Add(context.Background(), plan.PolicyRules[0]); err != nil {
		t.Fatalf("add server rule: %v", err)
	}
	if _, err := rules.Add(context.Background(), plan.PolicyRules[1]); err != nil {
		t.Fatalf("add default rule: %v", err)
	}

	want := [][]string{
		{"ip", "-4", "route", "add", "default", "dev", "tunwarden0", "table", "51820"},
		{"ip", "-4", "route", "add", "203.0.113.10/32", "via", "192.0.2.1", "dev", "eth0", "table", "main"},
		{"ip", "-4", "rule", "add", "priority", "51819", "to", "203.0.113.10/32", "lookup", "main"},
		{"ip", "-4", "rule", "add", "priority", "51820", "from", "all", "lookup", "51820"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", want, runner.commands)
	}
}

func TestIPRouteVerifyChecksDeviceAndGateway(t *testing.T) {
	route := planner.TunRoutePlan{Destination: "203.0.113.10/32", Table: planner.MainRoutingTable, Interface: "eth0", Gateway: "192.0.2.1", Action: "add"}
	good := &recordingRunner{stdout: "203.0.113.10 via 192.0.2.1 dev eth0 src 192.0.2.20"}
	if err := (IPRouteExecutor{Runner: good}).Verify(context.Background(), route); err != nil {
		t.Fatalf("expected route verify success: %v", err)
	}
	bad := &recordingRunner{stdout: "203.0.113.10 via 192.0.2.254 dev wlan0"}
	if err := (IPRouteExecutor{Runner: bad}).Verify(context.Background(), route); err == nil {
		t.Fatal("expected route verify mismatch")
	}
}

func TestIPPolicyRuleVerifyChecksSelectorAndLookupTable(t *testing.T) {
	rule := planner.TunPolicyRulePlan{Priority: planner.ServerRulePriority, Selector: "to 203.0.113.10/32", Table: planner.MainRoutingTable, Action: "add"}
	good := &recordingRunner{stdout: "51819: to 203.0.113.10 lookup main"}
	if err := (IPPolicyRuleExecutor{Runner: good}).Verify(context.Background(), rule); err != nil {
		t.Fatalf("expected rule verify success: %v", err)
	}
	bad := &recordingRunner{stdout: "51819: from all lookup 51820"}
	if err := (IPPolicyRuleExecutor{Runner: bad}).Verify(context.Background(), rule); err == nil {
		t.Fatal("expected rule verify mismatch")
	}
}

func TestIPRouteAddDoesNotReplaceExistingRoute(t *testing.T) {
	runner := &recordingRunner{err: errors.New("RTNETLINK answers: File exists")}
	route := planner.TunRoutePlan{Destination: "203.0.113.10/32", Table: planner.MainRoutingTable, Interface: "eth0", Gateway: "192.0.2.1", Action: "add"}
	if _, err := (IPRouteExecutor{Runner: runner}).Add(context.Background(), route); err == nil {
		t.Fatal("expected add to fail instead of replacing an existing route")
	}
	want := []string{"ip", "-4", "route", "add", "203.0.113.10/32", "via", "192.0.2.1", "dev", "eth0", "table", "main"}
	if !reflect.DeepEqual(runner.commands[0], want) {
		t.Fatalf("unexpected command: %#v", runner.commands[0])
	}
}

type callRecorder struct {
	calls []string
}

type fakeTun struct {
	rec *callRecorder
}

func (f fakeTun) Create(_ context.Context, plan planner.TunDevicePlan) (Step, error) {
	f.rec.calls = append(f.rec.calls, "tun:create:"+plan.Name)
	return Step{Kind: "tun-device", Target: plan.Name, Owner: OwnerTunDevice}, nil
}

func (f fakeTun) Verify(_ context.Context, plan planner.TunDevicePlan) error {
	f.rec.calls = append(f.rec.calls, "tun:verify:"+plan.Name)
	return nil
}

func (f fakeTun) Rollback(_ context.Context, plan planner.TunDevicePlan) error {
	f.rec.calls = append(f.rec.calls, "tun:rollback:"+plan.Name)
	return nil
}

type fakeRoutes struct {
	rec        *callRecorder
	failTarget string
}

func (f fakeRoutes) Add(_ context.Context, plan planner.TunRoutePlan) (Step, error) {
	target := plan.Table + ":" + plan.Destination
	f.rec.calls = append(f.rec.calls, "route:add:"+target)
	if target == f.failTarget {
		return Step{}, errors.New("injected route failure")
	}
	return Step{Kind: "route", Target: routeTarget(plan), Owner: OwnerRoute}, nil
}

func (f fakeRoutes) Verify(_ context.Context, plan planner.TunRoutePlan) error {
	f.rec.calls = append(f.rec.calls, "route:verify:"+plan.Table+":"+plan.Destination)
	return nil
}

func (f fakeRoutes) Rollback(_ context.Context, plan planner.TunRoutePlan) error {
	f.rec.calls = append(f.rec.calls, "route:rollback:"+plan.Table+":"+plan.Destination)
	return nil
}

type fakeRules struct {
	rec *callRecorder
}

func (f fakeRules) Add(_ context.Context, plan planner.TunPolicyRulePlan) (Step, error) {
	f.rec.calls = append(f.rec.calls, "rule:add:"+ruleCallTarget(plan))
	return Step{Kind: "policy-rule", Target: ruleTarget(plan), Owner: OwnerPolicyRule}, nil
}

func (f fakeRules) Verify(_ context.Context, plan planner.TunPolicyRulePlan) error {
	f.rec.calls = append(f.rec.calls, "rule:verify:"+ruleCallTarget(plan))
	return nil
}

func (f fakeRules) Rollback(_ context.Context, plan planner.TunPolicyRulePlan) error {
	f.rec.calls = append(f.rec.calls, "rule:rollback:"+ruleCallTarget(plan))
	return nil
}

type recordingRunner struct {
	commands [][]string
	stdout   string
	err      error
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := append([]string{name}, args...)
	r.commands = append(r.commands, command)
	if r.err != nil {
		return CommandResult{ExitCode: 2, Stderr: r.err.Error()}, r.err
	}
	return CommandResult{Stdout: r.stdout}, nil
}

func executorPlanForTest() planner.TunPlan {
	return planner.TunPlan{
		TunDevice: planner.TunDevicePlan{Name: "tunwarden0", MTU: 1500, Action: "create"},
		Routes: []planner.TunRoutePlan{
			{Family: "ipv4", Destination: "default", Table: planner.TunRoutingTable, Interface: "tunwarden0", Action: "add"},
			{Family: "ipv4", Destination: "203.0.113.10/32", Table: planner.MainRoutingTable, Interface: "eth0", Gateway: "192.0.2.1", Action: "add"},
		},
		PolicyRules: []planner.TunPolicyRulePlan{
			{Family: "ipv4", Priority: planner.ServerRulePriority, Selector: "to 203.0.113.10/32", Table: planner.MainRoutingTable, Action: "add"},
			{Family: "ipv4", Priority: planner.TunRulePriority, Selector: planner.IPv4DefaultSelector, Table: planner.TunRoutingTable, Action: "add"},
		},
	}
}

func ruleCallTarget(plan planner.TunPolicyRulePlan) string {
	return strconv.Itoa(plan.Priority) + ":" + plan.Selector
}
