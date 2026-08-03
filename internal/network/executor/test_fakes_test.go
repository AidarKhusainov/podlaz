package executor

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

var errRouteTestFailure = errors.New("route test failure")

type executorTestExitError struct {
	code int
}

func (e executorTestExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.code)
}

func (e executorTestExitError) ExitCode() int {
	return e.code
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
	skipTarget string
}

func (f fakeRoutes) Add(_ context.Context, plan planner.TunRoutePlan) (Step, error) {
	target := plan.Table + ":" + plan.Destination
	f.rec.calls = append(f.rec.calls, "route:add:"+target)
	if target == f.failTarget {
		return Step{}, errRouteTestFailure
	}
	if target == f.skipTarget {
		return Step{}, nil
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
	results  []CommandResult
	errs     []error
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := append([]string{name}, args...)
	r.commands = append(r.commands, command)
	idx := len(r.commands) - 1
	if idx < len(r.errs) && r.errs[idx] != nil {
		result := CommandResult{ExitCode: 2, Stderr: r.errs[idx].Error(), RawStderr: r.errs[idx].Error()}
		if idx < len(r.results) {
			result = withRawTestCommandOutput(r.results[idx])
		}
		return result, r.errs[idx]
	}
	if idx < len(r.results) {
		return withRawTestCommandOutput(r.results[idx]), nil
	}
	if r.err != nil {
		return CommandResult{ExitCode: 2, Stderr: r.err.Error(), RawStderr: r.err.Error()}, r.err
	}
	return CommandResult{Stdout: r.stdout, RawStdout: r.stdout}, nil
}

func withRawTestCommandOutput(result CommandResult) CommandResult {
	if result.RawStdout == "" && result.RawStderr == "" {
		result.RawStdout = result.Stdout
		result.RawStderr = result.Stderr
	}
	return result
}

func executorPlanForTest() planner.TunPlan {
	return planner.TunPlan{
		TunDevice: planner.TunDevicePlan{Name: "podlaz0", MTU: 1500, Action: "create"},
		Routes: []planner.TunRoutePlan{
			{Family: "ipv4", Destination: "default", Table: planner.TunRoutingTable, Interface: "podlaz0", Action: "add"},
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

func containsCommandToken(command []string, want string) bool {
	for _, token := range command {
		if token == want {
			return true
		}
	}
	return false
}

type fakeTunAddress struct {
	rec *callRecorder
}

func (f fakeTunAddress) Bind(_ context.Context, plan planner.TunAddressPlan) (planner.TunAddressPlan, error) {
	f.rec.calls = append(f.rec.calls, "address:bind:"+plan.Interface)
	plan.LinkIndex = 7
	plan.LinkKind = "tun"
	plan.AppearedAfterCore = true
	return plan, nil
}

func (f fakeTunAddress) Apply(_ context.Context, plan planner.TunAddressPlan) (Step, error) {
	f.rec.calls = append(f.rec.calls, "address:apply:"+plan.Interface+":"+plan.CIDR)
	return Step{Kind: "tun-address", Target: plan.Interface + ":" + plan.CIDR, Owner: OwnerTunAddress}, nil
}

func (f fakeTunAddress) Verify(_ context.Context, plan planner.TunAddressPlan) error {
	f.rec.calls = append(f.rec.calls, "address:verify:"+plan.Interface+":"+plan.CIDR)
	return nil
}

func (f fakeTunAddress) Rollback(_ context.Context, plan planner.TunAddressPlan) error {
	f.rec.calls = append(f.rec.calls, "address:rollback:"+plan.Interface+":"+plan.CIDR)
	return nil
}
