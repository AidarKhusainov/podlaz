package executor

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestDNSAwareTunRollbackStopsBeforeDependentPhasesWhenFirewallFails(t *testing.T) {
	recorder := &callRecorder{}
	firewallErr := errors.New("synthetic exact firewall rollback failure")
	exec := DNSAwareTunExecutor{
		Base: TunExecutor{
			TunDevice:   fakeTun{rec: recorder},
			TunAddress:  fakeTunAddress{rec: recorder},
			Routes:      fakeRoutes{rec: recorder},
			PolicyRules: fakeRules{rec: recorder},
		},
		DNS:      fakeDNS{rec: recorder},
		Firewall: rollbackFailFirewall{rec: recorder, err: firewallErr},
	}
	plan := executorPlanForTest()
	plan.TunAddress = rollbackIdentityAddressPlanForTest()
	plan.DNS = dnsPlanForTest()
	plan.Firewall = firewallPlanForTest()

	err := exec.Rollback(context.Background(), plan)
	if !errors.Is(err, firewallErr) {
		t.Fatalf("rollback error=%v, want firewall blocker %v", err, firewallErr)
	}
	want := []string{"firewall:rollback:inet podlaz"}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("firewall blocker must stop dependent rollback phases:\nwant %#v\n got %#v", want, recorder.calls)
	}
}

func TestDNSAwareTunRollbackStopsBeforeRoutesRulesAndAddressWhenDNSFails(t *testing.T) {
	recorder := &callRecorder{}
	dnsErr := errors.New("synthetic exact DNS rollback failure")
	exec := DNSAwareTunExecutor{
		Base: TunExecutor{
			TunDevice:   fakeTun{rec: recorder},
			TunAddress:  fakeTunAddress{rec: recorder},
			Routes:      fakeRoutes{rec: recorder},
			PolicyRules: fakeRules{rec: recorder},
		},
		DNS:      rollbackFailDNS{rec: recorder, err: dnsErr},
		Firewall: fakeFirewall{rec: recorder},
	}
	plan := executorPlanForTest()
	plan.TunAddress = rollbackIdentityAddressPlanForTest()
	plan.DNS = dnsPlanForTest()
	plan.Firewall = firewallPlanForTest()

	err := exec.Rollback(context.Background(), plan)
	if !errors.Is(err, dnsErr) {
		t.Fatalf("rollback error=%v, want DNS blocker %v", err, dnsErr)
	}
	want := []string{
		"firewall:rollback:inet podlaz",
		"dns:rollback:podlaz0",
	}
	if !reflect.DeepEqual(recorder.calls, want) {
		t.Fatalf("DNS blocker must stop routes/rules/address rollback:\nwant %#v\n got %#v", want, recorder.calls)
	}
}

type rollbackFailFirewall struct {
	rec *callRecorder
	err error
}

func (f rollbackFailFirewall) Apply(context.Context, planner.TunFirewallPlan) (Step, error) {
	return Step{}, errors.New("unexpected firewall apply")
}

func (f rollbackFailFirewall) Verify(context.Context, planner.TunFirewallPlan) error {
	return errors.New("unexpected firewall verify")
}

func (f rollbackFailFirewall) Rollback(_ context.Context, plan planner.TunFirewallPlan) error {
	f.rec.calls = append(f.rec.calls, "firewall:rollback:"+plan.Family+" "+plan.Table)
	return f.err
}

type rollbackFailDNS struct {
	rec *callRecorder
	err error
}

func (f rollbackFailDNS) Apply(context.Context, planner.TunDNSPlan) (Step, error) {
	return Step{}, errors.New("unexpected DNS apply")
}

func (f rollbackFailDNS) Verify(context.Context, planner.TunDNSPlan) error {
	return errors.New("unexpected DNS verify")
}

func (f rollbackFailDNS) Rollback(_ context.Context, plan planner.TunDNSPlan) error {
	f.rec.calls = append(f.rec.calls, "dns:rollback:"+plan.TargetLink)
	return f.err
}
