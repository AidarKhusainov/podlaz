package executor

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
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
	if len(steps) != 7 {
		t.Fatalf("expected TUN, route, policy-rule, DNS, and firewall steps, got %#v", steps)
	}
	if err := exec.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify firewall-aware plan: %v", err)
	}
	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback firewall-aware plan: %v", err)
	}

	want := []string{
		"tun:create:tunwarden0",
		"route:add:tunwarden:default",
		"route:add:main:203.0.113.10/32",
		"rule:add:9999:to 203.0.113.10/32",
		"rule:add:10000:from all",
		"dns:apply:tunwarden0",
		"firewall:apply:inet tunwarden",
		"tun:verify:tunwarden0",
		"route:verify:tunwarden:default",
		"route:verify:main:203.0.113.10/32",
		"rule:verify:9999:to 203.0.113.10/32",
		"rule:verify:10000:from all",
		"dns:verify:tunwarden0",
		"firewall:verify:inet tunwarden",
		"firewall:rollback:inet tunwarden",
		"dns:rollback:tunwarden0",
		"rule:rollback:10000:from all",
		"rule:rollback:9999:to 203.0.113.10/32",
		"route:rollback:main:203.0.113.10/32",
		"route:rollback:tunwarden:default",
		"tun:rollback:tunwarden0",
	}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("unexpected calls:\nwant %#v\n got %#v", want, recorder.calls)
	}
}

func TestDNSAwareTunExecutorRollsBackFirewallWhenFirewallApplyFails(t *testing.T) {
	recorder := &callRecorder{}
	exec := DNSAwareTunExecutor{
		Base:     TunExecutor{TunDevice: fakeTun{rec: recorder}, Routes: fakeRoutes{rec: recorder}, PolicyRules: fakeRules{rec: recorder}},
		DNS:      fakeDNS{rec: recorder},
		Firewall: fakeFirewall{rec: recorder, applyErr: errors.New("nft failure")},
	}
	plan := executorPlanForTest()
	plan.DNS = dnsPlanForTest()
	plan.Firewall = firewallPlanForTest()

	steps, err := exec.Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("expected firewall apply failure")
	}
	if len(steps) != 6 {
		t.Fatalf("expected base networking and DNS steps to remain rollbackable, got %#v", steps)
	}
	if got := recorder.calls[len(recorder.calls)-1]; got != "firewall:rollback:inet tunwarden" {
		t.Fatalf("expected immediate firewall rollback after apply failure, got %q", got)
	}
}

type fakeFirewall struct {
	rec      *callRecorder
	applyErr error
}

func (f fakeFirewall) Apply(_ context.Context, plan planner.TunFirewallPlan) (Step, error) {
	target := plan.Family + " " + plan.Table
	f.rec.calls = append(f.rec.calls, "firewall:apply:"+target)
	if f.applyErr != nil {
		return Step{}, f.applyErr
	}
	return Step{Kind: "nftables", Target: target, Owner: OwnerFirewall}, nil
}

func (f fakeFirewall) Verify(_ context.Context, plan planner.TunFirewallPlan) error {
	f.rec.calls = append(f.rec.calls, "firewall:verify:"+plan.Family+" "+plan.Table)
	return nil
}

func (f fakeFirewall) Rollback(_ context.Context, plan planner.TunFirewallPlan) error {
	f.rec.calls = append(f.rec.calls, "firewall:rollback:"+plan.Family+" "+plan.Table)
	return nil
}
