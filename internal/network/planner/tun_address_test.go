package planner

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestPlanTunIncludesDeterministicDaemonOwnedIPv4Address(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeResolvedDesktop())
	if err != nil {
		t.Fatalf("plan tun: %v", err)
	}

	if plan.TunAddress.Family != "ipv4" || plan.TunAddress.Interface != snapshot.DefaultTunName {
		t.Fatalf("unexpected TUN address identity: %#v", plan.TunAddress)
	}
	if plan.TunAddress.CIDR != DefaultTunIPv4CIDR || plan.TunAddress.Action != TunAddressActionAssign {
		t.Fatalf("unexpected TUN address policy: %#v", plan.TunAddress)
	}
	if plan.TunAddress.Owner != TunAddressOwner || plan.TunAddress.RollbackKey == "" {
		t.Fatalf("expected explicit address ownership: %#v", plan.TunAddress)
	}
}

func TestPlanTunBlocksExactAssignedAddressConflict(t *testing.T) {
	s := snapshot.FakeResolvedDesktop()
	s.IPv4Addresses.Addresses = append(s.IPv4Addresses.Addresses, snapshot.IPAddress{
		Family:    "ipv4",
		Interface: "eth1",
		CIDR:      DefaultTunIPv4CIDR,
		Scope:     "global",
	})

	plan, err := PlanTun(testVLESSProfile(), s)
	if err != nil {
		t.Fatalf("plan tun: %v", err)
	}

	if plan.TunAddress.Action != TunAddressActionBlocked || plan.TunAddress.Classification != TunAddressConflictClassification {
		t.Fatalf("expected exact address conflict, got %#v", plan.TunAddress)
	}
	if !strings.Contains(plan.TunAddress.Reason, "eth1") || !strings.Contains(plan.TunAddress.Reason, DefaultTunIPv4CIDR) {
		t.Fatalf("expected sanitized exact conflict evidence, got %q", plan.TunAddress.Reason)
	}
}

func TestPlanTunBlocksOverlappingNonDefaultRouteConflict(t *testing.T) {
	s := snapshot.FakeResolvedDesktop()
	s.IPv4Routes.Routes = append(s.IPv4Routes.Routes, snapshot.Route{
		Status:      snapshot.StatusDetected,
		Family:      "ipv4",
		Destination: "198.18.0.0/15",
		Interface:   "eth1",
		Table:       "100",
	})

	plan, err := PlanTun(testVLESSProfile(), s)
	if err != nil {
		t.Fatalf("plan tun: %v", err)
	}

	if plan.TunAddress.Action != TunAddressActionBlocked || plan.TunAddress.Classification != TunAddressConflictClassification {
		t.Fatalf("expected route overlap conflict, got %#v", plan.TunAddress)
	}
	if !strings.Contains(plan.TunAddress.Reason, "198.18.0.0/15") || !strings.Contains(plan.TunAddress.Reason, "table 100") {
		t.Fatalf("expected route conflict evidence, got %q", plan.TunAddress.Reason)
	}
}

func TestPlanTunIgnoresUnrelatedAddressRoutesAndDefaultRoute(t *testing.T) {
	s := snapshot.FakeResolvedDesktop()
	s.IPv4Addresses.Addresses = append(s.IPv4Addresses.Addresses, snapshot.IPAddress{Family: "ipv4", Interface: "eth1", CIDR: "203.0.113.20/24", Scope: "global"})
	s.IPv4Routes.Routes = append(s.IPv4Routes.Routes,
		snapshot.Route{Status: snapshot.StatusDetected, Family: "ipv4", Destination: "203.0.113.0/24", Interface: "eth1", Table: "100"},
		snapshot.Route{Status: snapshot.StatusDetected, Family: "ipv4", Destination: "default", Interface: "wlan0", Table: "main"},
	)

	plan, err := PlanTun(testVLESSProfile(), s)
	if err != nil {
		t.Fatalf("plan tun: %v", err)
	}

	if plan.TunAddress.Action != TunAddressActionAssign {
		t.Fatalf("unrelated host state must not block address assignment: %#v", plan.TunAddress)
	}
}

func TestPlanTunMarksPermissionLimitedAddressInspectionForDaemonRecheck(t *testing.T) {
	s := snapshot.FakeResolvedDesktop()
	s.IPv4Addresses = snapshot.IPAddressInventory{Inspection: snapshot.Finding{
		Status:  snapshot.StatusUnknown,
		Summary: "IPv4 address inventory unavailable",
		Detail:  "operation not permitted",
	}}

	plan, err := PlanTun(testVLESSProfile(), s)
	if err != nil {
		t.Fatalf("plan tun: %v", err)
	}

	if plan.TunAddress.Action != TunAddressActionDaemonRecheck {
		t.Fatalf("permission-limited local plan must require daemon re-check, got %#v", plan.TunAddress)
	}
	if plan.TunAddress.Classification != "" {
		t.Fatalf("incomplete local inspection must not claim a production conflict: %#v", plan.TunAddress)
	}
}

func TestPlanTunRollsBackAddressAfterRoutesBeforeLink(t *testing.T) {
	plan, err := PlanTun(testVLESSProfile(), snapshot.FakeResolvedDesktop())
	if err != nil {
		t.Fatalf("plan tun: %v", err)
	}

	address := rollbackStepIndex(plan.RollbackSteps, "Remove exact daemon-owned TUN address")
	routes := rollbackStepIndex(plan.RollbackSteps, "Delete route")
	link := rollbackStepIndex(plan.RollbackSteps, "Delete TUN interface")
	if routes < 0 || address < 0 || link < 0 {
		t.Fatalf("missing rollback steps: %#v", plan.RollbackSteps)
	}
	if !(routes < address && address < link) {
		t.Fatalf("expected routes/rules -> address -> link rollback order, got %#v", plan.RollbackSteps)
	}
}

func rollbackStepIndex(steps []string, needle string) int {
	for i, step := range steps {
		if strings.Contains(step, needle) {
			return i
		}
	}
	return -1
}
