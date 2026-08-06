package daemon

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

const productionLocalTunRouteRawForTest = "local 198.18.0.1 dev podlaz0 table local proto kernel scope host"

func TestSnapshotWithoutAllowedTunAddressConflictsKeepsSameInterfaceForeignOverlap(t *testing.T) {
	allowed := []tunAddressPreflightAllowance{exactPodlazTunAddressAllowance(planner.DefaultTunIPv4CIDR)}
	snapshot := netsnapshot.Snapshot{
		IPv4Addresses: netsnapshot.IPAddressInventory{
			Addresses: []netsnapshot.IPAddress{
				{Family: "ipv4", Interface: netsnapshot.DefaultTunName, CIDR: planner.DefaultTunIPv4CIDR, Scope: "global"},
				{Family: "ipv4", Interface: netsnapshot.DefaultTunName, CIDR: "198.18.0.2/32", Scope: "global"},
			},
		},
		IPv4Routes: netsnapshot.RouteInventory{
			Routes: []netsnapshot.Route{
				{Family: "ipv4", Interface: netsnapshot.DefaultTunName, Destination: planner.DefaultTunIPv4CIDR, Table: "local", Raw: productionLocalTunRouteRawForTest},
				{Family: "ipv4", Interface: netsnapshot.DefaultTunName, Destination: "198.18.0.0/24", Table: "main", Raw: "198.18.0.0/24 dev podlaz0"},
			},
		},
	}

	filtered := snapshotWithoutAllowedTunAddressConflicts(snapshot, planner.DefaultTunIPv4CIDR, allowed)

	if got := len(filtered.IPv4Addresses.Addresses); got != 1 {
		t.Fatalf("expected only foreign same-interface address to remain, got %d: %#v", got, filtered.IPv4Addresses.Addresses)
	}
	if filtered.IPv4Addresses.Addresses[0].CIDR != "198.18.0.2/32" {
		t.Fatalf("expected foreign /32 blocker to remain, got %#v", filtered.IPv4Addresses.Addresses)
	}
	if got := len(filtered.IPv4Routes.Routes); got != 1 {
		t.Fatalf("expected only foreign same-interface route to remain, got %d: %#v", got, filtered.IPv4Routes.Routes)
	}
	if filtered.IPv4Routes.Routes[0].Destination != "198.18.0.0/24" {
		t.Fatalf("expected foreign /24 route blocker to remain, got %#v", filtered.IPv4Routes.Routes)
	}
}

func TestSnapshotWithoutAllowedTunAddressConflictsAllowsExactLocalRouteOnly(t *testing.T) {
	allowed := []tunAddressPreflightAllowance{exactPodlazTunAddressAllowance(planner.DefaultTunIPv4CIDR)}
	snapshot := netsnapshot.Snapshot{
		IPv4Addresses: netsnapshot.IPAddressInventory{
			Addresses: []netsnapshot.IPAddress{
				{Family: "ipv4", Interface: netsnapshot.DefaultTunName, CIDR: planner.DefaultTunIPv4CIDR, Scope: "global"},
			},
		},
		IPv4Routes: netsnapshot.RouteInventory{
			Routes: []netsnapshot.Route{
				{Family: "ipv4", Interface: netsnapshot.DefaultTunName, Destination: planner.DefaultTunIPv4CIDR, Table: "local", Raw: productionLocalTunRouteRawForTest},
			},
		},
	}

	filtered := snapshotWithoutAllowedTunAddressConflicts(snapshot, planner.DefaultTunIPv4CIDR, allowed)

	if len(filtered.IPv4Addresses.Addresses) != 0 {
		t.Fatalf("expected exact persisted address to be allowed, got %#v", filtered.IPv4Addresses.Addresses)
	}
	if len(filtered.IPv4Routes.Routes) != 0 {
		t.Fatalf("expected exact local route to be allowed, got %#v", filtered.IPv4Routes.Routes)
	}
}

func TestParsedProductionLocalRouteCanBeAllowed(t *testing.T) {
	routes, err := netsnapshot.ParseIPv4Routes(productionLocalTunRouteRawForTest)
	if err != nil {
		t.Fatalf("parse production local route: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("parsed route count = %d, want 1", len(routes))
	}
	if routes[0].Table != "local" {
		t.Fatalf("parsed route table = %q, want local", routes[0].Table)
	}
	allowed := []tunAddressPreflightAllowance{exactPodlazTunAddressAllowance(planner.DefaultTunIPv4CIDR)}
	snapshot := netsnapshot.Snapshot{
		IPv4Addresses: netsnapshot.IPAddressInventory{
			Addresses: []netsnapshot.IPAddress{{Family: "ipv4", Interface: netsnapshot.DefaultTunName, CIDR: planner.DefaultTunIPv4CIDR, Scope: "global"}},
		},
		IPv4Routes: netsnapshot.RouteInventory{Routes: routes},
	}

	filtered := snapshotWithoutAllowedTunAddressConflicts(snapshot, planner.DefaultTunIPv4CIDR, allowed)
	if len(filtered.IPv4Addresses.Addresses) != 0 || len(filtered.IPv4Routes.Routes) != 0 {
		t.Fatalf("parsed production local route was not allowed: %#v", filtered)
	}
}
