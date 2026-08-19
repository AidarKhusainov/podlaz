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

func TestPlanTunForSessionUsesExactExistingServerRouteAsUnownedPrerequisite(t *testing.T) {
	s := snapshot.FakeDesktopWithServerRouteViaForeignVPN()
	s.IPv4Routes.Routes = append(s.IPv4Routes.Routes, snapshot.Route{
		Status:      snapshot.StatusDetected,
		Family:      "ipv4",
		Destination: "203.0.113.10/32",
		Table:       MainRoutingTable,
		Interface:   "wg0",
	})

	plan, err := PlanTunForSession(testVLESSProfile(), s, TunOptions{})
	if err != nil {
		t.Fatalf("PlanTunForSession() error = %v", err)
	}
	if plan.ServerBypass.Action != TunActionVerifyExisting {
		t.Fatalf("existing exact host route must be a verify-only prerequisite: %#v", plan.ServerBypass)
	}
	if plan.ServerBypass.Interface != "wg0" || plan.ServerBypass.Gateway != "" {
		t.Fatalf("existing server prerequisite identity changed: %#v", plan.ServerBypass)
	}
	for _, step := range plan.RollbackSteps {
		if step == "Delete IPv4 route 203.0.113.10/32 from table main" {
			t.Fatalf("unowned server prerequisite leaked into rollback steps: %#v", plan.RollbackSteps)
		}
	}
}

func TestPlanTunForSessionAllowsDegradedSoftBaselineWhenBootstrapAndAllocationEvidenceAreSafe(t *testing.T) {
	s := snapshot.FakeDesktopWithServerRouteViaForeignVPN()
	// The global/default-route diagnostic is intentionally degraded, but the
	// server-specific bootstrap route and the exact allocation inventories remain
	// authoritative. This is a non-pristine baseline, not proof that allocation
	// or bootstrap is unsafe.
	s.DefaultIPv4.Status = snapshot.StatusUnknown
	s.DefaultIPv4.Interface = ""
	s.DefaultIPv4.Gateway = ""
	s.NetworkManager.ActiveConnectionsInspection.Status = snapshot.StatusUnknown
	s.Warnings = append(s.Warnings, "synthetic unrelated baseline diagnostic is degraded")

	plan, err := PlanTunForSession(testVLESSProfile(), s, TunOptions{})
	if err != nil {
		t.Fatalf("degraded soft baseline with safe bootstrap must remain plannable: %v", err)
	}
	if plan.ServerBypass.Interface != "wg0" || plan.ServerBypass.Action != "add" {
		t.Fatalf("safe server bootstrap path was not preserved: %#v", plan.ServerBypass)
	}
	if plan.DNS.Action == DNSActionBlocked || plan.Firewall.TableAction == FirewallActionBlocked {
		t.Fatalf("unrelated degraded diagnostics must not block an otherwise safe plan: DNS=%#v firewall=%#v", plan.DNS, plan.Firewall)
	}
}
