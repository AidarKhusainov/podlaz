package daemon

import (
	"fmt"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTunRevalidationPlanRestoresCompleteDesiredStateWithoutExpandingRollbackAuthority(t *testing.T) {
	tx := issue245DesiredRevalidationTransaction()

	plan, err := tunRevalidationPlanFromTransaction(tx)
	if err != nil {
		t.Fatalf("restore revalidation plan: %v", err)
	}
	if plan.TunDevice.Action != "verify" || plan.TunDevice.Name != "podlaz0" {
		t.Fatalf("unexpected TUN device plan: %#v", plan.TunDevice)
	}
	if plan.TunAddress.CIDR != planner.DefaultTunIPv4CIDR || plan.TunAddress.LinkIndex != 9 {
		t.Fatalf("unexpected TUN address plan: %#v", plan.TunAddress)
	}
	if len(plan.Routes) != 2 {
		t.Fatalf("desired routes=%d, want 2: %#v", len(plan.Routes), plan.Routes)
	}
	if len(tx.Rollback.Routes) != 0 {
		t.Fatalf("test fixture unexpectedly grants route rollback authority: %#v", tx.Rollback.Routes)
	}
	if plan.ServerBypass.Destination != "203.0.113.10/32" || plan.ServerBypass.Interface != "wlan0" {
		t.Fatalf("unexpected server bypass: %#v", plan.ServerBypass)
	}
	if len(plan.PolicyRules) != 2 {
		t.Fatalf("desired policy rules=%d, want 2: %#v", len(plan.PolicyRules), plan.PolicyRules)
	}
	if len(tx.Rollback.PolicyRules) != 0 {
		t.Fatalf("test fixture unexpectedly grants policy-rule rollback authority: %#v", tx.Rollback.PolicyRules)
	}
	if plan.DNS.Action != planner.DNSActionConfigure || plan.DNS.TargetLink != "podlaz0" {
		t.Fatalf("unexpected DNS plan: %#v", plan.DNS)
	}
}

func TestTunRevalidationPlanRestoresExactOwnedFirewallRules(t *testing.T) {
	tx := issue245DesiredRevalidationTransaction()

	plan, err := tunRevalidationPlanFromTransaction(tx)
	if err != nil {
		t.Fatalf("restore revalidation plan: %v", err)
	}
	if len(plan.Firewall.Chains) != 1 || len(plan.Firewall.Rules) != 1 {
		t.Fatalf("unexpected firewall plan: %#v", plan.Firewall)
	}
	rule := plan.Firewall.Rules[0]
	if rule.Chain != "output" || rule.Expr != "oifname podlaz0" || rule.Verdict != "accept" || rule.Ownership != "podlaz:firewall:tun-egress" || rule.RollbackKey != planner.FirewallTunEgressKey {
		t.Fatalf("firewall rule was not restored exactly: %#v", rule)
	}
}

func TestTunRevalidationPlanRejectsFirewallRuleWithoutOwnershipMarker(t *testing.T) {
	tx := issue245DesiredRevalidationTransaction()
	tx.DesiredPlan.NFT.Chains[0].Rules = []string{"oifname podlaz0 accept"}
	if _, err := tunRevalidationPlanFromTransaction(tx); err == nil {
		t.Fatal("expected malformed persisted firewall rule to fail closed")
	}
}

func TestTunRevalidationPlanRejectsMalformedDesiredPolicyRule(t *testing.T) {
	tx := issue245DesiredRevalidationTransaction()
	tx.DesiredPlan.Steps[0].Target = "priority invalid to 203.0.113.10/32 lookup main"
	if _, err := tunRevalidationPlanFromTransaction(tx); err == nil {
		t.Fatal("expected malformed desired policy rule to fail closed")
	}
}

func issue245DesiredRevalidationTransaction() txstate.Transaction {
	return txstate.Transaction{
		Mode:      planner.ModeTun,
		ProfileID: "profile-test",
		DesiredPlan: txstate.DesiredPlan{
			TUN: txstate.TUNDesiredState{
				InterfaceName: "podlaz0",
				MTU:           1500,
				Owner:         xrayTunInboundOwner,
			},
			TUNAddress: txstate.TUNAddressDesiredState{
				Family:            "ipv4",
				InterfaceName:     "podlaz0",
				CIDR:              planner.DefaultTunIPv4CIDR,
				Scope:             "global",
				LinkIndex:         9,
				LinkKind:          "tun",
				AppearedAfterCore: true,
				Owner:             netexecutor.OwnerTunAddress,
			},
			Routes: []txstate.RoutePlan{
				{
					Kind:      "route",
					Table:     planner.TunRoutingTable,
					CIDR:      planner.IPv4DefaultRoute,
					Dev:       "podlaz0",
					Owner:     netexecutor.OwnerRoute,
					Operation: "add",
				},
				{
					Kind:      "route",
					Table:     planner.MainRoutingTable,
					CIDR:      "203.0.113.10/32",
					Via:       "192.0.2.1",
					Dev:       "wlan0",
					Owner:     netexecutor.OwnerRoute,
					Operation: "add",
				},
			},
			DNS: txstate.DNSPlan{
				Backend:       planner.DNSBackendSystemdResolved,
				Link:          "podlaz0",
				Servers:       []string{"192.0.2.53"},
				SearchDomains: []string{"~."},
				Owner:         txstate.TransactionOwner,
			},
			NFT: txstate.NFTPlan{
				Family: "inet",
				Table:  "podlaz",
				Owner:  netexecutor.OwnerFirewall,
				Chains: []txstate.NFTChainPlan{{
					Name:     "output",
					Type:     "filter",
					Hook:     "output",
					Priority: 0,
					Policy:   "accept",
					Owner:    netexecutor.OwnerFirewall,
					Rules:    []string{"oifname podlaz0 accept owner podlaz:firewall:tun-egress"},
				}},
			},
			Steps: []txstate.PlannedStep{
				{
					Kind:   "policy-rule",
					Target: fmt.Sprintf("priority %d to 203.0.113.10/32 lookup %s", planner.ServerRulePriority, planner.MainRoutingTable),
					Owner:  netexecutor.OwnerPolicyRule,
				},
				{
					Kind:   "policy-rule",
					Target: fmt.Sprintf("priority %d from all lookup %s", planner.TunRulePriority, planner.TunRoutingTable),
					Owner:  netexecutor.OwnerPolicyRule,
				},
			},
		},
		Rollback: txstate.RollbackMetadata{
			NFTables: []txstate.NFTablesRollback{{
				Family: "inet",
				Table:  "podlaz",
				Owner:  netexecutor.OwnerFirewall,
			}},
		},
	}
}
