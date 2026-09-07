package daemon

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestResolvedLinkReadinessAcceptsXrayOwnedAllocatedSession(t *testing.T) {
	plan := addressedTunPlanForConnectivityTest()
	plan.Routes = []planner.TunRoutePlan{
		{Family: "ipv4", Destination: planner.IPv4DefaultRoute, Table: "51820", Interface: "podlaz0", Action: "add"},
		{Family: "ipv4", Destination: "203.0.113.10/32", Table: planner.MainRoutingTable, Interface: "eth0", Gateway: "192.0.2.1", Action: "add"},
	}
	plan.PolicyRules = []planner.TunPolicyRulePlan{
		{Family: "ipv4", Priority: 9999, Selector: "to 203.0.113.10/32", Table: planner.MainRoutingTable, Action: "add"},
		{Family: "ipv4", Priority: 10000, Selector: planner.IPv4DefaultSelector, Table: "51820", Action: "add"},
	}

	plan = xrayOwnedTunPlan(plan)
	if plan.TunAddress.Action != planner.TunAddressActionAssignExclusive {
		t.Fatalf("xray-owned allocated TUN address action = %q, want %q", plan.TunAddress.Action, planner.TunAddressActionAssignExclusive)
	}
	if err := validateResolvedLinkReadiness(plan); err != nil {
		t.Fatalf("resolved-link readiness rejected the production allocated TUN action: %v", err)
	}
}
