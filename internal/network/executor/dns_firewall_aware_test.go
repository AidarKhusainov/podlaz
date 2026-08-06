package executor

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestDNSAwareTunExecutorAppliesVerifiesAndRollsBackFirewallInSafeOrder(t *testing.T) {
	recorder := &callRecorder{}
	exec := DNSAwareTunExecutor{
		Base:     TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}},
		DNS:      fakeDNS{rec: recorder},
		Firewall: fakeFirewall{rec: recorder},
	}
	plan := executorPlanForTest()
	plan.DNS = dnsPlanForTest()
	plan.Firewall = firewallPlanForTest()

	steps, err := exec.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply firewall-aware plan: %v", err)
	}
	if len(steps) != 6 {
		t.Fatalf("expected route, policy-rule, DNS, and firewall steps without TUN ownership, got %#v", steps)
	}
	for _, step := range steps {
		if step.Kind == "tun-device" {
			t.Fatalf("Xray-owned TUN link must not create daemon ownership step: %#v", steps)
		}
	}
	if err := exec.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify firewall-aware plan: %v", err)
	}
	addRollbackIdentityForTest(&exec, &plan, recorder)
	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback firewall-aware plan: %v", err)
	}

	want := []string{
		"tun:verify:podlaz0",
		"route:add:podlaz:default",
		"route:add:main:203.0.113.10/32",
		"rule:add:9999:to 203.0.113.10/32",
		"rule:add:10000:from all",
		"dns:apply:podlaz0",
		"firewall:apply:inet podlaz",
		"tun:verify:podlaz0",
		"route:verify:podlaz:default",
		"route:verify:main:203.0.113.10/32",
		"rule:verify:9999:to 203.0.113.10/32",
		"rule:verify:10000:from all",
		"dns:verify:podlaz0",
		"firewall:verify:inet podlaz",
		"firewall:rollback:inet podlaz",
		"dns:rollback:podlaz0",
		"rule:rollback:10000:from all",
		"rule:rollback:9999:to 203.0.113.10/32",
		"route:rollback:main:203.0.113.10/32",
		"route:rollback:podlaz:default",
		"address:rollback:podlaz0:198.18.0.1/32",
	}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("unexpected calls:\nwant %#v\n got %#v", want, recorder.calls)
	}
}

func TestDNSAwareTunExecutorPreservesPartialFirewallOwnershipWithoutInternalRollback(t *testing.T) {
	recorder := &callRecorder{}
	exec := DNSAwareTunExecutor{
		Base:     TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}},
		DNS:      fakeDNS{rec: recorder},
		Firewall: fakeFirewall{rec: recorder, applyErr: errors.New("nft failure"), returnStepOnError: true},
	}
	plan := executorPlanForTest()
	plan.DNS = dnsPlanForTest()
	plan.Firewall = firewallPlanForTest()

	steps, err := exec.Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("expected firewall apply failure")
	}
	if len(steps) != 6 || steps[len(steps)-1].Kind != "nftables" || steps[len(steps)-1].Owner != OwnerFirewall {
		t.Fatalf("expected partial firewall step to remain rollbackable without TUN ownership, got %#v", steps)
	}
	if got := recorder.calls[len(recorder.calls)-1]; got != "firewall:apply:inet podlaz" {
		t.Fatalf("composition executor must not rollback before transaction diagnostics, got %q", got)
	}
}

type fakeFirewall struct {
	rec               *callRecorder
	applyErr          error
	returnStepOnError bool
}

func (f fakeFirewall) Apply(_ context.Context, plan planner.TunFirewallPlan) (Step, error) {
	target := plan.Family + " " + plan.Table
	f.rec.calls = append(f.rec.calls, "firewall:apply:"+target)
	step := Step{Kind: "nftables", Target: target, Owner: OwnerFirewall}
	if f.applyErr != nil {
		if f.returnStepOnError {
			return step, f.applyErr
		}
		return Step{}, f.applyErr
	}
	return step, nil
}

func (f fakeFirewall) Verify(_ context.Context, plan planner.TunFirewallPlan) error {
	f.rec.calls = append(f.rec.calls, "firewall:verify:"+plan.Family+" "+plan.Table)
	return nil
}

func (f fakeFirewall) Rollback(_ context.Context, plan planner.TunFirewallPlan) error {
	f.rec.calls = append(f.rec.calls, "firewall:rollback:"+plan.Family+" "+plan.Table)
	return nil
}

func TestDNSAwareTunExecutorApplyWithStepSinkPersistsDNSBeforeFirewall(t *testing.T) {
	recorder := &callRecorder{}
	exec := DNSAwareTunExecutor{
		Base:     TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}},
		DNS:      fakeDNS{rec: recorder},
		Firewall: fakeFirewall{rec: recorder},
	}
	plan := executorPlanForTest()
	plan.DNS = dnsPlanForTest()
	plan.Firewall = firewallPlanForTest()

	_, err := exec.ApplyWithStepSink(context.Background(), plan, func(step Step) error {
		recorder.calls = append(recorder.calls, "persist:"+step.Kind)
		return nil
	})
	if err != nil {
		t.Fatalf("apply DNS-aware plan with sink: %v", err)
	}

	wantTail := []string{
		"dns:apply:podlaz0",
		"persist:dns",
		"firewall:apply:inet podlaz",
		"persist:nftables",
	}
	if got := recorder.calls[len(recorder.calls)-len(wantTail):]; !reflect.DeepEqual(got, wantTail) {
		t.Fatalf("DNS ownership must be durable before firewall mutation:\nwant %#v\n got %#v", wantTail, got)
	}
}

func TestDNSAwareTunExecutorApplyWithStepSinkPersistsPartialDNSOwnershipBeforeReturningError(t *testing.T) {
	recorder := &callRecorder{}
	exec := DNSAwareTunExecutor{
		Base:     TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}},
		DNS:      fakeDNS{rec: recorder, applyErr: errors.New("resolved failure"), returnStepOnError: true},
		Firewall: fakeFirewall{rec: recorder},
	}
	plan := executorPlanForTest()
	plan.DNS = dnsPlanForTest()
	plan.Firewall = firewallPlanForTest()

	steps, err := exec.ApplyWithStepSink(context.Background(), plan, func(step Step) error {
		recorder.calls = append(recorder.calls, "persist:"+step.Kind)
		return nil
	})
	if err == nil {
		t.Fatal("expected DNS apply failure")
	}
	if len(steps) == 0 || steps[len(steps)-1].Kind != "dns" {
		t.Fatalf("partial DNS mutation must remain rollbackable, got %#v", steps)
	}
	wantTail := []string{"dns:apply:podlaz0", "persist:dns"}
	if got := recorder.calls[len(recorder.calls)-len(wantTail):]; !reflect.DeepEqual(got, wantTail) {
		t.Fatalf("partial DNS ownership must be persisted before returning error:\nwant %#v\n got %#v", wantTail, got)
	}
}
