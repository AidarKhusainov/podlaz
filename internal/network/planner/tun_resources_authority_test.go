package planner

import (
	"net/netip"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestAllocateTunResourcesUsesTypedAllocationAuthority(t *testing.T) {
	evidence := snapshot.TunAllocationEvidence{
		IPv4Addresses: []netip.Prefix{netip.MustParsePrefix(DefaultTunIPv4CIDR)},
		IPv4Routes: []snapshot.TunAllocationRoute{
			{Destination: netip.MustParsePrefix("172.17.0.0/16"), Table: 254},
			{Default: true, Table: TunRoutingTableID},
		},
		IPv4PolicyRules: []snapshot.TunAllocationRule{
			{Priority: 100, Table: 60000},
		},
	}

	allocation, err := AllocateTunResources(evidence)
	if err != nil {
		t.Fatalf("AllocateTunResources() error = %v", err)
	}
	if allocation.TunIPv4CIDR == DefaultTunIPv4CIDR {
		t.Fatalf("allocator reused occupied TUN address: %#v", allocation)
	}
	if allocation.RoutingTableID == TunRoutingTableID {
		t.Fatalf("allocator reused occupied routing table: %#v", allocation)
	}
	if allocation.ServerRulePriority != 98 || allocation.TunnelRulePriority != 99 {
		t.Fatalf("allocator did not precede foreign priority 100: %#v", allocation)
	}
}

func TestAllocateTunResourcesFailsClosedOnInvalidTypedAuthority(t *testing.T) {
	_, err := AllocateTunResources(snapshot.TunAllocationEvidence{
		IPv4Routes: []snapshot.TunAllocationRoute{{Default: true, Table: 0}},
	})
	if err == nil {
		t.Fatal("unspecified routing table must block allocation")
	}
}

func TestAllocateTunResourcesRejectsPoolOverlappedByForeignRoute(t *testing.T) {
	_, err := AllocateTunResources(snapshot.TunAllocationEvidence{
		IPv4Routes: []snapshot.TunAllocationRoute{{Destination: netip.MustParsePrefix("198.18.0.0/15"), Table: 254}},
	})
	if err == nil {
		t.Fatal("foreign route covering the bounded TUN pool must block allocation")
	}
}

func TestPlanTunForSessionWithAllocationEvidenceSeparatesDiagnosticsFromAuthority(t *testing.T) {
	diagnostic := snapshot.FakeDesktopWithServerRouteViaForeignVPN()
	diagnostic.IPv4Addresses.Inspection.Status = snapshot.StatusUnknown
	diagnostic.IPv4Routes.Inspection.Status = snapshot.StatusUnknown
	diagnostic.IPv4PolicyRules.Inspection.Status = snapshot.StatusUnknown

	evidence := snapshot.TunAllocationEvidence{
		IPv4Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")},
		IPv4Routes: []snapshot.TunAllocationRoute{{Default: true, Table: 254}},
		IPv4PolicyRules: []snapshot.TunAllocationRule{{Priority: 100, Table: 60000}},
	}

	plan, err := PlanTunForSessionWithAllocationEvidence(testVLESSProfile(), diagnostic, evidence, TunOptions{})
	if err != nil {
		t.Fatalf("PlanTunForSessionWithAllocationEvidence() error = %v", err)
	}
	if plan.ServerBypass.Interface != "wg0" {
		t.Fatalf("diagnostic server path was not preserved: %#v", plan.ServerBypass)
	}
	if plan.PolicyRules[0].Priority != 98 || plan.PolicyRules[1].Priority != 99 {
		t.Fatalf("typed allocation authority was not used: %#v", plan.PolicyRules)
	}
}
