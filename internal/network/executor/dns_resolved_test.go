package executor

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const resolvedStatusForTest = "Link 7 (podlaz0)\n    DNS Servers: 1.1.1.1\n    DNS Domain: ~."

func TestResolvedDNSExecutorApplyVerifyAndRollbackCommands(t *testing.T) {
	runner := &recordingRunner{stdout: resolvedStatusForTest}
	exec := ResolvedDNSExecutor{Runner: runner}
	plan := dnsPlanForTest()

	step, err := exec.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply DNS: %v", err)
	}
	if step.Kind != "dns" || step.Target != "podlaz0" || step.Owner != OwnerDNS {
		t.Fatalf("unexpected DNS step: %#v", step)
	}
	if err := exec.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify DNS: %v", err)
	}
	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback DNS: %v", err)
	}

	want := [][]string{
		{"resolvectl", "dns", "podlaz0", "1.1.1.1"},
		{"resolvectl", "domain", "podlaz0", "~."},
		{"resolvectl", "default-route", "podlaz0", "yes"},
		{"resolvectl", "status", "podlaz0", "--no-pager"},
		{"resolvectl", "revert", "podlaz0"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", want, runner.commands)
	}
}

func TestResolvedDNSExecutorFailsClearlyWhenPlanIsBlocked(t *testing.T) {
	plan := dnsPlanForTest()
	plan.Action = planner.DNSActionBlocked
	plan.Reason = "systemd-resolved state is missing"

	_, err := (ResolvedDNSExecutor{Runner: &recordingRunner{}}).Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("expected blocked DNS plan failure")
	}
}

func TestResolvedDNSExecutorVerifyRequiresRouteOnlyDomain(t *testing.T) {
	plan := dnsPlanForTest()
	err := (ResolvedDNSExecutor{Runner: &recordingRunner{stdout: "Link 7 (podlaz0)"}}).Verify(context.Background(), plan)
	if err == nil {
		t.Fatal("expected verify failure when route-only domain is missing")
	}
}

func TestResolvedDNSExecutorRequiresPlannedServers(t *testing.T) {
	plan := dnsPlanForTest()
	plan.Servers = nil
	_, err := (ResolvedDNSExecutor{Runner: &recordingRunner{}}).Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("expected missing DNS servers failure")
	}
}

func TestDNSAwareTunExecutorAppliesVerifiesAndRollsBackDNSInSafeOrder(t *testing.T) {
	recorder := &callRecorder{}
	exec := DNSAwareTunExecutor{
		Base: TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}},
		DNS:  fakeDNS{rec: recorder},
	}
	plan := executorPlanForTest()
	plan.DNS = dnsPlanForTest()

	steps, err := exec.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply DNS-aware plan: %v", err)
	}
	if len(steps) != 6 {
		t.Fatalf("expected TUN, route, policy-rule, and DNS steps, got %#v", steps)
	}
	if err := exec.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify DNS-aware plan: %v", err)
	}
	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback DNS-aware plan: %v", err)
	}

	want := dnsAwareCallOrderForTest()
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("unexpected calls:\nwant %#v\n got %#v", want, recorder.calls)
	}
}

func TestDNSAwareTunExecutorRollsBackDNSWhenApplyFailsAfterDNSMutation(t *testing.T) {
	recorder := &callRecorder{}
	exec := DNSAwareTunExecutor{
		Base: TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}},
		DNS:  fakeDNS{rec: recorder, applyErr: errors.New("resolved failure")},
	}
	plan := executorPlanForTest()
	plan.DNS = dnsPlanForTest()

	steps, err := exec.Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("expected DNS apply failure")
	}
	if len(steps) != 5 {
		t.Fatalf("expected base networking steps to remain rollbackable, got %#v", steps)
	}
	if recorder.calls[len(recorder.calls)-1] != "dns:rollback:podlaz0" {
		t.Fatalf("expected immediate DNS rollback after DNS apply failure, got %#v", recorder.calls)
	}
}

func dnsAwareCallOrderForTest() []string {
	return []string{
		"tun:create:podlaz0",
		"route:add:podlaz:default",
		"route:add:main:203.0.113.10/32",
		"rule:add:9999:to 203.0.113.10/32",
		"rule:add:10000:from all",
		"dns:apply:podlaz0",
		"tun:verify:podlaz0",
		"route:verify:podlaz:default",
		"route:verify:main:203.0.113.10/32",
		"rule:verify:9999:to 203.0.113.10/32",
		"rule:verify:10000:from all",
		"dns:verify:podlaz0",
		"dns:rollback:podlaz0",
		"rule:rollback:10000:from all",
		"rule:rollback:9999:to 203.0.113.10/32",
		"route:rollback:main:203.0.113.10/32",
		"route:rollback:podlaz:default",
		"tun:rollback:podlaz0",
	}
}

func dnsPlanForTest() planner.TunDNSPlan {
	return planner.TunDNSPlan{
		Backend:    planner.DNSBackendSystemdResolved,
		TargetLink: "podlaz0",
		Servers:    []string{planner.DefaultTunDNSServer},
		Action:     planner.DNSActionConfigure,
		Reason:     "use systemd-resolved per-link DNS",
	}
}

type fakeDNS struct {
	rec      *callRecorder
	applyErr error
}

func (f fakeDNS) Apply(_ context.Context, plan planner.TunDNSPlan) (Step, error) {
	f.rec.calls = append(f.rec.calls, "dns:apply:"+plan.TargetLink)
	if f.applyErr != nil {
		return Step{}, f.applyErr
	}
	return Step{Kind: "dns", Target: plan.TargetLink, Owner: OwnerDNS}, nil
}

func (f fakeDNS) Verify(_ context.Context, plan planner.TunDNSPlan) error {
	f.rec.calls = append(f.rec.calls, "dns:verify:"+plan.TargetLink)
	return nil
}

func (f fakeDNS) Rollback(_ context.Context, plan planner.TunDNSPlan) error {
	f.rec.calls = append(f.rec.calls, "dns:rollback:"+plan.TargetLink)
	return nil
}
