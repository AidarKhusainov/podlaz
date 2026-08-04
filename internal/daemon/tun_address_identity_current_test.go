package daemon

import (
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestSnapshotProvesExactTunAddressRequiresCurrentLinkKindAndKernelLocalRoute(t *testing.T) {
	address := txstate.TUNAddressRollback{
		Family:            "ipv4",
		InterfaceName:     netsnapshot.DefaultTunName,
		CIDR:              planner.DefaultTunIPv4CIDR,
		Scope:             "global",
		LinkIndex:         7,
		LinkKind:          "tun",
		AppearedAfterCore: true,
		Owner:             netexecutor.OwnerTunAddress,
	}

	valid := netsnapshot.FakeResolvedDesktop()
	valid.TunDevices = []netsnapshot.TunDevice{{
		Name:   netsnapshot.DefaultTunName,
		Status: netsnapshot.StatusDetected,
		Raw:    "7: podlaz0: <POINTOPOINT,UP> mtu 1500 tun type tun",
	}}
	valid.IPv4Addresses = netsnapshot.IPAddressInventory{
		Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected},
		Addresses: []netsnapshot.IPAddress{{
			Family:    "ipv4",
			Interface: netsnapshot.DefaultTunName,
			CIDR:      planner.DefaultTunIPv4CIDR,
			Scope:     "global",
		}},
	}
	valid.IPv4Routes = netsnapshot.RouteInventory{
		Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected},
		Routes: []netsnapshot.Route{{
			Status:      netsnapshot.StatusDetected,
			Family:      "ipv4",
			Destination: planner.DefaultTunIPv4CIDR,
			Interface:   netsnapshot.DefaultTunName,
			Table:       "local",
			Raw:         "local 198.18.0.1 dev podlaz0 table local proto kernel scope host",
		}},
	}
	if !snapshotProvesExactTunAddress(address, valid) {
		t.Fatal("expected real kernel local route and current tun kind to prove exact stale address")
	}

	wrongKind := valid
	wrongKind.TunDevices = []netsnapshot.TunDevice{{
		Name:   netsnapshot.DefaultTunName,
		Status: netsnapshot.StatusDetected,
		Raw:    "7: podlaz0: <BROADCAST,UP> mtu 1500 ether type veth",
	}}
	if snapshotProvesExactTunAddress(address, wrongKind) {
		t.Fatal("foreign replacement with matching ifindex but non-tun current kind must not prove ownership")
	}

	manualRoute := valid
	manualRoute.IPv4Routes.Routes[0].Raw = "local 198.18.0.1 dev podlaz0 table local scope host"
	if snapshotProvesExactTunAddress(address, manualRoute) {
		t.Fatal("manual local-table route without proto kernel evidence must not prove ownership")
	}
}
