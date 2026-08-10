package daemon

import (
	"fmt"
	"testing"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestTunUplinkFingerprintIgnoresPodlazOwnedState(t *testing.T) {
	baseline := issue245UplinkSnapshot()
	changed := issue245UplinkSnapshot()
	changed.TunDevices = []netsnapshot.TunDevice{{Name: netsnapshot.DefaultTunName, Status: netsnapshot.StatusDetected}}
	changed.PolicyRouting = []netsnapshot.PolicyRoutingSignal{{Kind: "rule", Priority: "10000", Table: "51820"}}
	changed.Nftables.PodlazTable = netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "podlaz nftables table exists"}
	changed.DNS.ResolvedLinks = append(changed.DNS.ResolvedLinks, netsnapshot.ResolvedLink{Index: "9", Name: netsnapshot.DefaultTunName})

	before, err := deriveTunUplinkFingerprint(baseline, issue245InterfaceIndex)
	if err != nil {
		t.Fatalf("derive baseline fingerprint: %v", err)
	}
	after, err := deriveTunUplinkFingerprint(changed, issue245InterfaceIndex)
	if err != nil {
		t.Fatalf("derive changed fingerprint: %v", err)
	}
	if before != after {
		t.Fatalf("podlaz-owned state changed uplink fingerprint:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestTunUplinkFingerprintDetectsMaterialUnderlyingChanges(t *testing.T) {
	baseline, err := deriveTunUplinkFingerprint(issue245UplinkSnapshot(), issue245InterfaceIndex)
	if err != nil {
		t.Fatalf("derive baseline fingerprint: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*netsnapshot.Snapshot, *int)
	}{
		{name: "gateway", mutate: func(s *netsnapshot.Snapshot, _ *int) {
			s.DefaultIPv4.Gateway = "192.0.2.254"
			s.ServerRoute.Gateway = "192.0.2.254"
		}},
		{name: "interface", mutate: func(s *netsnapshot.Snapshot, _ *int) {
			s.DefaultIPv4.Interface = "eth0"
			s.ServerRoute.Interface = "eth0"
			s.IPv4Addresses.Addresses = []netsnapshot.IPAddress{{Family: "ipv4", Interface: "eth0", CIDR: "192.0.2.55/24", Scope: "global"}}
			s.NetworkManager.ActiveConnections[0].Device = "eth0"
		}},
		{name: "address", mutate: func(s *netsnapshot.Snapshot, _ *int) {
			s.IPv4Addresses.Addresses[0].CIDR = "192.0.2.99/24"
		}},
		{name: "interface index", mutate: func(_ *netsnapshot.Snapshot, index *int) { *index = 4 }},
		{name: "NetworkManager connection", mutate: func(s *netsnapshot.Snapshot, _ *int) {
			s.NetworkManager.ActiveConnections[0].UUID = "22222222-3333-4444-5555-666666666666"
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := issue245UplinkSnapshot()
			index := 3
			tc.mutate(&snapshot, &index)
			got, err := deriveTunUplinkFingerprint(snapshot, func(name string) (int, error) {
				if name != snapshot.DefaultIPv4.Interface {
					return 0, fmt.Errorf("unexpected interface %q", name)
				}
				return index, nil
			})
			if err != nil {
				t.Fatalf("derive changed fingerprint: %v", err)
			}
			if got == baseline {
				t.Fatalf("material %s change did not change fingerprint: %+v", tc.name, got)
			}
		})
	}
}

func TestTunUplinkFingerprintFailsClosedOnAmbiguousEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*netsnapshot.Snapshot)
	}{
		{name: "default route missing", mutate: func(s *netsnapshot.Snapshot) {
			s.DefaultIPv4 = netsnapshot.Route{Status: netsnapshot.StatusMissing, Family: "ipv4", Destination: "default"}
		}},
		{name: "server route through TUN", mutate: func(s *netsnapshot.Snapshot) { s.ServerRoute.Interface = netsnapshot.DefaultTunName }},
		{name: "uplink address inspection incomplete", mutate: func(s *netsnapshot.Snapshot) { s.IPv4Addresses.Inspection.Status = netsnapshot.StatusUnknown }},
		{name: "server route moved to another uplink", mutate: func(s *netsnapshot.Snapshot) { s.ServerRoute.Interface = "eth0" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := issue245UplinkSnapshot()
			tc.mutate(&snapshot)
			if _, err := deriveTunUplinkFingerprint(snapshot, issue245InterfaceIndex); err == nil {
				t.Fatal("expected ambiguous uplink evidence to fail closed")
			}
		})
	}
}

func issue245InterfaceIndex(name string) (int, error) {
	switch name {
	case "wlan0", "eth0":
		return 3, nil
	default:
		return 0, fmt.Errorf("unexpected interface %q", name)
	}
}

func issue245UplinkSnapshot() netsnapshot.Snapshot {
	return netsnapshot.Snapshot{
		OS:          "linux",
		DefaultIPv4: netsnapshot.Route{Status: netsnapshot.StatusDetected, Family: "ipv4", Destination: "default", Interface: "wlan0", Gateway: "192.0.2.1"},
		ServerRoute: netsnapshot.Route{Status: netsnapshot.StatusDetected, Destination: "203.0.113.10", Interface: "wlan0", Gateway: "192.0.2.1"},
		IPv4Addresses: netsnapshot.IPAddressInventory{
			Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "IPv4 address inventory available"},
			Addresses:  []netsnapshot.IPAddress{{Family: "ipv4", Interface: "wlan0", CIDR: "192.0.2.55/24", Scope: "global"}},
		},
		DNS: netsnapshot.DNS{Mode: "systemd-resolved", Resolved: netsnapshot.Finding{Status: netsnapshot.StatusDetected}},
		NetworkManager: netsnapshot.NetworkManager{
			Finding:                     netsnapshot.Finding{Status: netsnapshot.StatusDetected},
			State:                       "connected",
			ActiveConnectionsInspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "active connection inventory available"},
			ActiveConnections: []netsnapshot.NetworkManagerConnection{{
				Name: "ExampleNetwork", UUID: "11111111-2222-3333-4444-555555555555", Type: "802-11-wireless", Device: "wlan0", State: "activated",
			}},
		},
		Nftables:   netsnapshot.Nftables{Availability: netsnapshot.Finding{Status: netsnapshot.StatusDetected}, PodlazTable: netsnapshot.Finding{Status: netsnapshot.StatusMissing}},
		TunDevices: []netsnapshot.TunDevice{{Name: netsnapshot.DefaultTunName, Status: netsnapshot.StatusMissing}},
	}
}
