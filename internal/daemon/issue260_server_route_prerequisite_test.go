package daemon

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestDesiredPlanPersistsExistingServerRouteAsUnownedPrerequisite(t *testing.T) {
	s := netsnapshot.FakeDesktopWithServerRouteViaForeignVPN()
	s.IPv4Routes.Routes = append(s.IPv4Routes.Routes, netsnapshot.Route{
		Status:      netsnapshot.StatusDetected,
		Family:      "ipv4",
		Destination: "203.0.113.10/32",
		Table:       planner.MainRoutingTable,
		Interface:   "wg0",
	})
	plan, err := planner.PlanTunForSession(testVLESSProfile(), s, planner.TunOptions{})
	if err != nil {
		t.Fatalf("PlanTunForSession() error = %v", err)
	}
	plan = xrayOwnedTunPlan(plan)

	desired := desiredPlanFromTunPlan(plan)
	rollback := rollbackMetadataFromTunPlan(plan)

	foundPrerequisite := false
	for _, route := range desired.Routes {
		if route.CIDR != "203.0.113.10/32" || route.Table != planner.MainRoutingTable {
			continue
		}
		foundPrerequisite = true
		if route.Operation != "verify" || route.Owner != hostRoutePrerequisiteOwner || route.Dev != "wg0" || route.Via != "" {
			t.Fatalf("unexpected persisted server prerequisite: %#v", route)
		}
	}
	if !foundPrerequisite {
		t.Fatalf("desired plan did not persist exact server prerequisite: %#v", desired.Routes)
	}
	for _, route := range rollback.Routes {
		if route.CIDR == "203.0.113.10/32" && route.Table == planner.MainRoutingTable {
			t.Fatalf("unowned server prerequisite must not grant rollback authority: %#v", rollback.Routes)
		}
	}
}
