package executor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const resolvedStatusForTest = `Global
       Protocols: +LLMNR +mDNS -DNSOverTLS DNSSEC=no/unsupported

Link 7 (podlaz0)
    Current Scopes: DNS
         Protocols: +DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
Current DNS Server: 1.1.1.1
       DNS Servers: 1.1.1.1
        DNS Domain: ~.`

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
		{"resolvectl", "revert", "podlaz0"},
		{"resolvectl", "dns", "podlaz0", "1.1.1.1"},
		{"resolvectl", "domain", "podlaz0", "~."},
		{"resolvectl", "default-route", "podlaz0", "yes"},
		{"resolvectl", "status", "--no-pager"},
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

func TestResolvedDNSExecutorVerifyRejectsForeignRouteOnlyDNSOwner(t *testing.T) {
	plan := dnsPlanForTest()
	err := (ResolvedDNSExecutor{Runner: &recordingRunner{stdout: resolvedStatusForTest + `

Link 9 (wg0)
    Current Scopes: DNS
Current DNS Server: 198.51.100.53
       DNS Servers: 198.51.100.53
        DNS Domain: ~.`}}).Verify(context.Background(), plan)
	if err == nil {
		t.Fatal("expected verify failure when foreign route-only DNS owner remains")
	}
}

func TestResolvedDNSExecutorVerifyToleratesResolvedPropagationDelay(t *testing.T) {
	plan := dnsPlanForTest()
	runner := &recordingRunner{results: []CommandResult{
		{Stdout: `Link 7 (podlaz0)
    Current Scopes: none
       DNS Servers: 1.1.1.1
        DNS Domain: ~.`},
		{Stdout: resolvedStatusForTest},
	}}
	exec := ResolvedDNSExecutor{
		Runner:             runner,
		VerifyAttempts:     2,
		VerifyPollInterval: time.Nanosecond,
		Sleep:              noResolvedDNSTestSleep,
	}

	if err := exec.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify DNS after propagation delay: %v", err)
	}
	if got := countResolvedStatusCommands(runner.commands); got != 2 {
		t.Fatalf("expected 2 status polls, got %d: %#v", got, runner.commands)
	}
}

func TestResolvedDNSExecutorVerifyRequiresDNSDefaultRoute(t *testing.T) {
	plan := dnsPlanForTest()
	err := (ResolvedDNSExecutor{Runner: &recordingRunner{stdout: `Link 7 (podlaz0)
    Current Scopes: none
       DNS Servers: 1.1.1.1
        DNS Domain: ~.`}, VerifyAttempts: 1}).Verify(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "DNS default route is not enabled") {
		t.Fatalf("expected DNS default-route failure, got %v", err)
	}
}

func TestResolvedDNSExecutorVerifyRequiresRouteOnlyDomain(t *testing.T) {
	plan := dnsPlanForTest()
	err := (ResolvedDNSExecutor{Runner: &recordingRunner{stdout: `Link 7 (podlaz0)
    Current Scopes: DNS
         Protocols: +DefaultRoute
       DNS Servers: 1.1.1.1`}, VerifyAttempts: 1}).Verify(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "route-only domain ~. not found") {
		t.Fatalf("expected route-only domain failure, got %v", err)
	}
}

func TestResolvedDNSExecutorVerifyRequiresPlannedDNSServer(t *testing.T) {
	plan := dnsPlanForTest()
	err := (ResolvedDNSExecutor{Runner: &recordingRunner{stdout: `Link 7 (podlaz0)
    Current Scopes: DNS
         Protocols: +DefaultRoute
       DNS Servers: 9.9.9.9
        DNS Domain: ~.`}, VerifyAttempts: 1}).Verify(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "DNS server 1.1.1.1 not found") {
		t.Fatalf("expected planned DNS server failure, got %v", err)
	}
}

func TestResolvedDNSExecutorVerifyRequiresTargetLink(t *testing.T) {
	plan := dnsPlanForTest()
	err := (ResolvedDNSExecutor{Runner: &recordingRunner{stdout: `Link 2 (wlan0)
    Current Scopes: DNS
         Protocols: +DefaultRoute
       DNS Servers: 1.1.1.1
        DNS Domain: corp.example.test`}, VerifyAttempts: 1}).Verify(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "link status not found") {
		t.Fatalf("expected target link failure, got %v", err)
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
	addRollbackIdentityForTest(&exec, &plan, recorder)
	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback DNS-aware plan: %v", err)
	}

	want := dnsAwareCallOrderForTest()
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("unexpected calls:\nwant %#v\n got %#v", want, recorder.calls)
	}
}

func TestDNSAwareTunExecutorPreservesPartialDNSOwnershipWithoutInternalRollback(t *testing.T) {
	recorder := &callRecorder{}
	exec := DNSAwareTunExecutor{
		Base: TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}},
		DNS:  fakeDNS{rec: recorder, applyErr: errors.New("resolved failure"), returnStepOnError: true},
	}
	plan := executorPlanForTest()
	plan.DNS = dnsPlanForTest()

	steps, err := exec.Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("expected DNS apply failure")
	}
	if len(steps) != 6 || steps[len(steps)-1].Kind != "dns" || steps[len(steps)-1].Owner != OwnerDNS {
		t.Fatalf("expected partial DNS step to remain rollbackable, got %#v", steps)
	}
	if got := recorder.calls[len(recorder.calls)-1]; got != "dns:apply:podlaz0" {
		t.Fatalf("composition executor must not rollback before transaction diagnostics, got %q", got)
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
		"address:rollback:podlaz0:198.18.0.1/32",
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
	rec               *callRecorder
	applyErr          error
	returnStepOnError bool
}

func (f fakeDNS) Apply(_ context.Context, plan planner.TunDNSPlan) (Step, error) {
	f.rec.calls = append(f.rec.calls, "dns:apply:"+plan.TargetLink)
	step := Step{Kind: "dns", Target: plan.TargetLink, Owner: OwnerDNS}
	if f.applyErr != nil {
		if f.returnStepOnError {
			return step, f.applyErr
		}
		return Step{}, f.applyErr
	}
	return step, nil
}

func (f fakeDNS) Verify(_ context.Context, plan planner.TunDNSPlan) error {
	f.rec.calls = append(f.rec.calls, "dns:verify:"+plan.TargetLink)
	return nil
}

func (f fakeDNS) Rollback(_ context.Context, plan planner.TunDNSPlan) error {
	f.rec.calls = append(f.rec.calls, "dns:rollback:"+plan.TargetLink)
	return nil
}

func noResolvedDNSTestSleep(context.Context, time.Duration) error {
	return nil
}

func countResolvedStatusCommands(commands [][]string) int {
	count := 0
	for _, command := range commands {
		if reflect.DeepEqual(command, []string{"resolvectl", "status", "--no-pager"}) {
			count++
		}
	}
	return count
}
