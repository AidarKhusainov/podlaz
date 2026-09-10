package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestTunExecutorApplyReportsTypedSubphase(t *testing.T) {
	cause := errors.New("apply failed")
	plan := planner.TunPlan{
		TunDevice:  planner.TunDevicePlan{Action: "verify", Name: "podlaz0"},
		TunAddress: planner.TunAddressPlan{Action: "assign", InterfaceName: "podlaz0", CIDR: "198.18.0.1/32"},
	}
	exec := TunExecutor{
		TunDevice:  applySubphaseTunDevice{},
		TunAddress: applySubphaseTunAddress{err: cause},
		Routes:     applySubphaseRoute{},
		PolicyRules: applySubphasePolicyRule{},
	}
	_, err := exec.Apply(context.Background(), plan)
	if !errors.Is(err, cause) {
		t.Fatalf("apply cause lost: %v", err)
	}
	if got := ApplyFailureSubphase(err); got != "tun-address" {
		t.Fatalf("apply subphase=%q want tun-address", got)
	}
}

func TestDNSAwareTunExecutorApplyReportsDNSAndNFTablesSubphases(t *testing.T) {
	for _, tt := range []struct {
		name string
		dnsErr error
		firewallErr error
		want string
	}{
		{name: "dns", dnsErr: errors.New("dns apply failed"), want: "dns"},
		{name: "nftables", firewallErr: errors.New("firewall apply failed"), want: "nftables"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := planner.TunPlan{
				TunDevice: planner.TunDevicePlan{Action: "verify", Name: "podlaz0"},
				DNS: planner.TunDNSPlan{TargetLink: "podlaz0", Servers: []string{"192.0.2.53"}},
				Firewall: planner.TunFirewallPlan{Action: "apply", Family: "inet", Table: "podlaz"},
			}
			exec := DNSAwareTunExecutor{
				Base: TunExecutor{TunDevice: applySubphaseTunDevice{}, Routes: applySubphaseRoute{}, PolicyRules: applySubphasePolicyRule{}},
				DNS: applySubphaseDNS{err: tt.dnsErr},
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
func (applySubphaseTunDevice) Create(context.Context, planner.TunDevicePlan) (Step, error) { return Step{}, nil }
func (applySubphaseTunDevice) Verify(context.Context, planner.TunDevicePlan) error { return nil }
func (applySubphaseTunDevice) Rollback(context.Context, planner.TunDevicePlan) error { return nil }

type applySubphaseTunAddress struct{ err error }
func (e applySubphaseTunAddress) Bind(context.Context, planner.TunAddressPlan, TunLinkCreationProof) (planner.TunAddressPlan, error) { return planner.TunAddressPlan{}, nil }
func (e applySubphaseTunAddress) Apply(context.Context, planner.TunAddressPlan) (Step, error) { return Step{}, e.err }
func (e applySubphaseTunAddress) Verify(context.Context, planner.TunAddressPlan) error { return nil }
func (e applySubphaseTunAddress) Rollback(context.Context, planner.TunAddressPlan) error { return nil }

type applySubphaseRoute struct{}
func (applySubphaseRoute) Add(context.Context, planner.TunRoutePlan) (Step, error) { return Step{}, nil }
func (applySubphaseRoute) Verify(context.Context, planner.TunRoutePlan) error { return nil }
func (applySubphaseRoute) Rollback(context.Context, planner.TunRoutePlan) error { return nil }

type applySubphasePolicyRule struct{}
func (applySubphasePolicyRule) Add(context.Context, planner.TunPolicyRulePlan) (Step, error) { return Step{}, nil }
func (applySubphasePolicyRule) Verify(context.Context, planner.TunPolicyRulePlan) error { return nil }
func (applySubphasePolicyRule) Rollback(context.Context, planner.TunPolicyRulePlan) error { return nil }

type applySubphaseDNS struct{ err error }
func (e applySubphaseDNS) Apply(context.Context, planner.TunDNSPlan) (Step, error) { return Step{}, e.err }
func (e applySubphaseDNS) Verify(context.Context, planner.TunDNSPlan) error { return nil }
func (e applySubphaseDNS) Rollback(context.Context, planner.TunDNSPlan) error { return nil }

type applySubphaseFirewall struct{ err error }
func (e applySubphaseFirewall) Apply(context.Context, planner.TunFirewallPlan) (Step, error) { return Step{}, e.err }
func (e applySubphaseFirewall) Verify(context.Context, planner.TunFirewallPlan) error { return nil }
func (e applySubphaseFirewall) Rollback(context.Context, planner.TunFirewallPlan) error { return nil }
