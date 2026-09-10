package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestTunExecutorApplyReportsTypedSubphase(t *testing.T) {
	for _, tt := range []struct {
		name string
		exec TunExecutor
		plan planner.TunPlan
		want string
	}{
		{
			name: "tun address",
			exec: TunExecutor{
				TunDevice:   applySubphaseTunDevice{},
				TunAddress:  applySubphaseTunAddress{err: errors.New("address apply failed")},
				Routes:      applySubphaseRoute{},
				PolicyRules: applySubphasePolicyRule{},
			},
			plan: func() planner.TunPlan {
				plan := executorPlanForTest()
				plan.TunAddress = planner.TunAddressPlan{Action: planner.TunAddressActionAssign, Interface: "podlaz0", CIDR: planner.DefaultTunIPv4CIDR}
				return plan
			}(),
			want: "tun-address",
		},
		{
			name: "routes",
			exec: TunExecutor{
				TunDevice:   applySubphaseTunDevice{},
				Routes:      applySubphaseRoute{err: errors.New("route apply failed")},
				PolicyRules: applySubphasePolicyRule{},
			},
			plan: executorPlanForTest(),
			want: "routes",
		},
		{
			name: "policy rules",
			exec: TunExecutor{
				TunDevice:   applySubphaseTunDevice{},
				Routes:      applySubphaseRoute{},
				PolicyRules: applySubphasePolicyRule{err: errors.New("policy rule apply failed")},
			},
			plan: executorPlanForTest(),
			want: "policy-rules",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.exec.Apply(context.Background(), tt.plan)
			if err == nil {
				t.Fatal("expected apply failure")
			}
			if got := ApplyFailureSubphase(err); got != tt.want {
				t.Fatalf("apply subphase=%q want=%q err=%v", got, tt.want, err)
			}
		})
	}
}

func TestDNSAwareTunExecutorApplyReportsDNSAndNFTablesSubphases(t *testing.T) {
	for _, tt := range []struct {
		name        string
		dnsErr      error
		firewallErr error
		withFirewall bool
		want        string
	}{
		{name: "dns", dnsErr: errors.New("dns apply failed"), want: "dns"},
		{name: "nftables", firewallErr: errors.New("firewall apply failed"), withFirewall: true, want: "nftables"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := executorPlanForTest()
			plan.DNS = planner.TunDNSPlan{TargetLink: "podlaz0", Servers: []string{"192.0.2.53"}, Action: planner.DNSActionConfigure}
			if tt.withFirewall {
				plan.Firewall = firewallPlanForTest()
			}
			exec := DNSAwareTunExecutor{
				Base: TunExecutor{
					TunDevice:   applySubphaseTunDevice{},
					Routes:      applySubphaseRoute{},
					PolicyRules: applySubphasePolicyRule{},
				},
				DNS:      applySubphaseDNS{err: tt.dnsErr},
				Firewall: applySubphaseFirewall{err: tt.firewallErr},
			}
			_, err := exec.Apply(context.Background(), plan)
			if err == nil {
				t.Fatal("expected apply failure")
			}
			if got := ApplyFailureSubphase(err); got != tt.want {
				t.Fatalf("apply subphase=%q want=%q err=%v", got, tt.want, err)
			}
		})
	}
}

type applySubphaseTunDevice struct{}

func (applySubphaseTunDevice) Create(context.Context, planner.TunDevicePlan) (Step, error) {
	return Step{}, nil
}
func (applySubphaseTunDevice) Verify(context.Context, planner.TunDevicePlan) error { return nil }
func (applySubphaseTunDevice) Rollback(context.Context, planner.TunDevicePlan) error { return nil }

type applySubphaseTunAddress struct{ err error }

func (e applySubphaseTunAddress) Bind(_ context.Context, plan planner.TunAddressPlan, _ TunLinkCreationProof) (planner.TunAddressPlan, error) {
	return plan, nil
}
func (e applySubphaseTunAddress) Apply(context.Context, planner.TunAddressPlan) (Step, error) {
	return Step{}, e.err
}
func (e applySubphaseTunAddress) Verify(context.Context, planner.TunAddressPlan) error { return nil }
func (e applySubphaseTunAddress) Rollback(context.Context, planner.TunAddressPlan) error { return nil }

type applySubphaseRoute struct{ err error }

func (e applySubphaseRoute) Add(context.Context, planner.TunRoutePlan) (Step, error) {
	return Step{}, e.err
}
func (e applySubphaseRoute) Verify(context.Context, planner.TunRoutePlan) error { return nil }
func (e applySubphaseRoute) Rollback(context.Context, planner.TunRoutePlan) error { return nil }

type applySubphasePolicyRule struct{ err error }

func (e applySubphasePolicyRule) Add(context.Context, planner.TunPolicyRulePlan) (Step, error) {
	return Step{}, e.err
}
func (e applySubphasePolicyRule) Verify(context.Context, planner.TunPolicyRulePlan) error { return nil }
func (e applySubphasePolicyRule) Rollback(context.Context, planner.TunPolicyRulePlan) error { return nil }

type applySubphaseDNS struct{ err error }

func (e applySubphaseDNS) Apply(context.Context, planner.TunDNSPlan) (Step, error) {
	return Step{}, e.err
}
func (e applySubphaseDNS) Verify(context.Context, planner.TunDNSPlan) error { return nil }
func (e applySubphaseDNS) Rollback(context.Context, planner.TunDNSPlan) error { return nil }

type applySubphaseFirewall struct{ err error }

func (e applySubphaseFirewall) Apply(context.Context, planner.TunFirewallPlan) (Step, error) {
	return Step{}, e.err
}
func (e applySubphaseFirewall) Verify(context.Context, planner.TunFirewallPlan) error { return nil }
func (e applySubphaseFirewall) Rollback(context.Context, planner.TunFirewallPlan) error { return nil }
