package planner

import (
	"strconv"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestAllocateTunResourcesPrefersHistoricalValuesWhenFree(t *testing.T) {
	allocation, err := AllocateTunResources(snapshot.FakeResolvedDesktop())
	if err != nil {
		t.Fatalf("AllocateTunResources() error = %v", err)
	}
	if allocation.TunIPv4CIDR != DefaultTunIPv4CIDR || allocation.RoutingTableID != TunRoutingTableID || allocation.ServerRulePriority != ServerRulePriority || allocation.TunnelRulePriority != TunRulePriority {
		t.Fatalf("unexpected clean allocation: %#v", allocation)
	}
}

func TestAllocateTunResourcesAvoidsHistoricalCollisionsAndPrecedesForeignRules(t *testing.T) {
	s := snapshot.FakeResolvedDesktop()
	s.IPv4Addresses.Addresses = append(s.IPv4Addresses.Addresses, snapshot.IPAddress{Family: "ipv4", Interface: "test0", CIDR: DefaultTunIPv4CIDR, Scope: "global"})
	s.IPv4Routes.Routes = append(s.IPv4Routes.Routes, snapshot.Route{Status: snapshot.StatusDetected, Family: "ipv4", Destination: "default", Table: strconv.Itoa(TunRoutingTableID), Interface: "test0"})
	s.IPv4PolicyRules.Rules = []snapshot.PolicyRoutingSignal{
		{Kind: "rule", Priority: strconv.Itoa(ServerRulePriority), Selector: "to 198.51.100.10", Table: "main"},
		{Kind: "rule", Priority: strconv.Itoa(TunRulePriority), Selector: "from all", Table: strconv.Itoa(TunRoutingTableID)},
		{Kind: "rule", Priority: "100", Selector: "from all", Table: "60000"},
	}

	allocation, err := AllocateTunResources(s)
	if err != nil {
		t.Fatalf("AllocateTunResources() error = %v", err)
	}
	if allocation.TunIPv4CIDR == DefaultTunIPv4CIDR {
		t.Fatalf("allocator reused occupied TUN address: %#v", allocation)
	}
	if allocation.RoutingTableID == TunRoutingTableID {
		t.Fatalf("allocator reused occupied routing table: %#v", allocation)
	}
	if allocation.ServerRulePriority >= 100 || allocation.TunnelRulePriority >= 100 {
		t.Fatalf("allocated rules must precede existing priority 100 rule: %#v", allocation)
	}
	if allocation.ServerRulePriority >= allocation.TunnelRulePriority {
		t.Fatalf("server bypass must precede full-tunnel rule: %#v", allocation)
	}
}

func TestAllocateTunResourcesFailsClosedWhenRequiredInventoryIsUnknown(t *testing.T) {
	s := snapshot.FakeResolvedDesktop()
	s.IPv4PolicyRules.Inspection.Status = snapshot.StatusUnknown
	if _, err := AllocateTunResources(s); err == nil {
		t.Fatal("expected unknown policy-rule inventory to block allocation")
	}
}

func TestPlanTunForSessionUsesAllocatedNumericIdentitiesAndActualServerPath(t *testing.T) {
	s := snapshot.FakeDesktopWithServerRouteViaForeignVPN()
	s.IPv4Routes.Routes = append(s.IPv4Routes.Routes, snapshot.Route{Status: snapshot.StatusDetected, Family: "ipv4", Destination: "default", Table: strconv.Itoa(TunRoutingTableID), Interface: "test0"})
	s.IPv4PolicyRules.Rules = []snapshot.PolicyRoutingSignal{
		{Kind: "rule", Priority: strconv.Itoa(ServerRulePriority), Selector: "to 198.51.100.10", Table: "main"},
		{Kind: "rule", Priority: strconv.Itoa(TunRulePriority), Selector: "from all", Table: strconv.Itoa(TunRoutingTableID)},
	}

	plan, err := PlanTunForSession(testVLESSProfile(), s, TunOptions{})
	if err != nil {
		t.Fatalf("PlanTunForSession() error = %v", err)
	}
	if plan.ServerBypass.Interface != "wg0" {
		t.Fatalf("server bootstrap must preserve actual current path, got %#v", plan.ServerBypass)
	}
	if plan.ServerBypass.Gateway != "" {
		t.Fatalf("fixture server path has no gateway, got %#v", plan.ServerBypass)
	}
	if len(plan.PolicyRules) != 2 || plan.PolicyRules[0].Priority == ServerRulePriority || plan.PolicyRules[1].Priority == TunRulePriority {
		t.Fatalf("expected reallocated policy rules, got %#v", plan.PolicyRules)
	}
	if len(plan.Routes) == 0 || plan.Routes[0].Table == TunRoutingTable || plan.Routes[0].Table == strconv.Itoa(TunRoutingTableID) {
		t.Fatalf("expected exact reallocated numeric routing table, got %#v", plan.Routes)
	}
}
