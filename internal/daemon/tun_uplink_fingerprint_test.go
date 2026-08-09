package daemon

import (
	"testing"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestTunUplinkFingerprintIgnoresPodlazOwnedState(t *testing.T) {
	baseline := issue245UplinkSnapshot()
	changed := issue245UplinkSnapshot()
	changed.TunDevices = []netsnapshot.TunDevice{{
		Name:   netsnapshot.DefaultTunName,
		Status: netsnapshot.StatusDetected,
		Detail: "9: podlaz0: <POINTOPOINT,UP> mtu 1500 tun type tun",
	}}
	changed.PolicyRouting = []netsnapshot.PolicyRoutingSignal{{
		Kind:     "rule",
		Priority: "10000",
		Table:    "51820",
	}}
	changed.Nftables.PodlazTable = netsnapshot.Finding{
		Status:  netsnapshot.StatusDetected,
		Summary: "podlaz nftables table exists",
	}
	changed.DNS.ResolvedLinks = append(changed.DNS.ResolvedLinks, netsnapshot.ResolvedLink{
		Index:            "9",
		Name:             netsnapshot.DefaultTunName,
		CurrentDNSServer: "198.51.100.53",
		DNSServers:       []string{"198.51.100.53"},
		DNSDomains:       []string{"~."},
	})

	before, err := deriveTunUplinkFingerprint(baseline)
	if err != nil {
		t.Fatalf("derive baseline fingerprint: %v", err)
	}
	after, err := deriveTunUplinkFingerprint(changed)
	if err != nil {
		t.Fatalf("derive changed fingerprint: %v", err)
	}
	if before != after {
		t.Fatalf("Podlaz-owned state changed uplink fingerprint:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestTunUplinkFingerprintDetectsMaterialUnderlyingChanges(t *testing.T) {
	baseline, err := deriveTunUplinkFingerprint(issue245UplinkSnapshot())
	if err != nil {
		t.Fatalf("derive baseline fingerprint: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*netsnapshot.Snapshot)
	}{
		{
			name: "gateway",
			mutate: func(s *netsnapshot.Snapshot) {
				s.DefaultIPv4.Gateway = "192.0.2.254"
				s.ServerRoute.Gateway = "192.0.2.254"
			},
		},
		{
			name: "interface",
			mutate: func(s *netsnapshot.Snapshot) {
				s.DefaultIPv4.Interface = "eth0"
				s.ServerRoute.Interface = "eth0"
				s.IPv4Addresses.Addresses = []netsnapshot.IPAddress{{Family: "ipv4", Interface: "eth0", CIDR: "192.0.2.55/24", Scope: "global"}}
				s.DNS.ResolvedLinks[0].Name = "eth0"
				s.NetworkManager.ActiveConnections[0].Device = "eth0"
			},
		},
		{
			name: "address",
			mutate: func(s *netsnapshot.Snapshot) {
				s.IPv4Addresses.Addresses[0].CIDR = "192.0.2.99/24"
			},
		},
		{
			name: "interface index",
			mutate: func(s *netsnapshot.Snapshot) {
				s.DNS.ResolvedLinks[0].Index = "4"
			},
		},
		{
			name: "NetworkManager connection",
			mutate: func(s *netsnapshot.Snapshot) {
				s.NetworkManager.ActiveConnections[0].UUID = "22222222-3333-4444-5555-666666666666"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := issue245UplinkSnapshot()
			tt.mutate(&snapshot)
			got, err := deriveTunUplinkFingerprint(snapshot)
			if err != nil {
				t.Fatalf("derive changed fingerprint: %v", err)
			}
			if got == baseline {
				t.Fatalf("material %s change did not change fingerprint: %+v", tt.name, got)
			}
		})
	}
}

func TestTunUplinkFingerprintFailsClosedOnAmbiguousOwnershipEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*netsnapshot.Snapshot)
	}{
		{
			name: "default route missing",
			mutate: func(s *netsnapshot.Snapshot) {
				s.DefaultIPv4 = netsnapshot.Route{Status: netsnapshot.StatusMissing, Family: "ipv4", Destination: "default"}
			},
		},
		{
			name: "server bypass through TUN",
			mutate: func(s *netsnapshot.Snapshot) {
				s.ServerRoute.Interface = netsnapshot.DefaultTunName
			},
		},
		{
			name: "uplink address inspection incomplete",
			mutate: func(s *netsnapshot.Snapshot) {
				s.IPv4Addresses.Inspection.Status = netsnapshot.StatusUnknown
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := issue245UplinkSnapshot()
			t.mutate(&snapshot)
			if _, err := deriveTunUplinkFingerprint(snapshot); err == nil {
				t.Fatal("expected ambiguous uplink evidence to fail closed")
			}
		})
	}
}

func issue245UplinkSnapshot() netsnapshot.Snapshot {
	return netsnapshot.Snapshot{
		OS: "linux",
		DefaultIPv4: netsnapshot.Route{
			Status:      netsnapshot.StatusDetected,
			Family:      "ipv4",
			Destination: "default",
			Interface:   "wlan0",
			Gateway:     "192.0.2.1",
		},
		ServerRoute: netsnapshot.Route{
			Status:      netsnapshot.StatusDetected,
			Destination: "203.0.113.10",
			Interface:   "wlan0",
			Gateway:     "192.0.2.1",
		},
		IPv4Addresses: netsnapshot.IPAddressInventory{
			Inspection: netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "IPv4 address inventory available"},
			Addresses: []netsnapshot.IPAddress{{
				Family:    "ipv4",
				Interface: "wlan0",
				CIDR:      "192.0.2.55/24",
				Scope:     "global",
			}},
		},
		DNS: netsnapshot.DNS{
			Mode:     "systemd-resolved",
			Resolved: netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "systemd-resolved status available"},
			ResolvedLinks: []netsnapshot.ResolvedLink{{
				Index: "3",
				Name:  "wlan0",
			}},
		},
		NetworkManager: netsnapshot.NetworkManager{
			Finding: netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "NetworkManager state available"},
			State:   "connected",
			ActiveConnections: []netsnapshot.NetworkManagerConnection{{
				Name:   "ExampleNetwork",
				UUID:   "11111111-2222-3333-4444-555555555555",
				Type:   "802-11-wireless",
				Device: "wlan0",
				State:  "activated",
			}},
		},
		Nftables: netsnapshot.Nftables{
			Availability: netsnapshot.Finding{Status: netsnapshot.StatusDetected, Summary: "nftables available"},
			PodlazTable:  netsnapshot.Finding{Status: netsnapshot.StatusMissing, Summary: "podlaz table missing"},
		},
		TunDevices: []netsnapshot.TunDevice{{Name: netsnapshot.DefaultTunName, Status: netsnapshot.StatusMissing}},
	}
}
