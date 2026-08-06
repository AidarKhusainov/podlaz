package snapshot

// FakeResolvedDesktop returns a common Linux desktop topology with systemd-resolved,
// NetworkManager, nftables, and no stale podlaz-owned resources.
func FakeResolvedDesktop() Snapshot {
	return Snapshot{
		OS: "linux",
		DefaultIPv4: Route{
			Status:      StatusDetected,
			Family:      "ipv4",
			Destination: "default",
			Interface:   "wlp0s20f3",
			Gateway:     "192.0.2.1",
			Raw:         "default via 192.0.2.1 dev wlp0s20f3 proto dhcp metric 600",
		},
		DefaultIPv6: Route{Status: StatusMissing, Family: "ipv6", Destination: "default", Detail: "route not found"},
		ServerRoute: Route{
			Status:      StatusDetected,
			Destination: "example.com",
			Interface:   "wlp0s20f3",
			Gateway:     "192.0.2.1",
			Raw:         "203.0.113.10 via 192.0.2.1 dev wlp0s20f3 src 192.0.2.55 uid 1000",
		},
		DNS: DNS{
			Mode:     "systemd-resolved",
			Resolved: Finding{Status: StatusDetected, Summary: "systemd-resolved status available", Detail: "Global"},
			ResolvedLinks: []ResolvedLink{{
				Index:            "3",
				Name:             "wlp0s20f3",
				CurrentScopes:    []string{"DNS"},
				Protocols:        []string{"+DefaultRoute", "+LLMNR", "-mDNS", "-DNSOverTLS", "DNSSEC=no/unsupported"},
				CurrentDNSServer: "192.0.2.53",
				DNSServers:       []string{"192.0.2.53"},
				DNSDomains:       []string{"lan.example.invalid"},
			}},
		},
		NetworkManager: NetworkManager{Finding: Finding{Status: StatusDetected, Summary: "NetworkManager state available", Detail: "running:connected"}, State: "connected"},
		Nftables: Nftables{
			Availability: Finding{Status: StatusDetected, Summary: "nftables table listing available"},
			PodlazTable:  Finding{Status: StatusMissing, Summary: "podlaz nftables table not found"},
		},
		TunDevices: []TunDevice{{Name: DefaultTunName, Status: StatusMissing, Detail: "device not found"}},
		IPv4Addresses: IPAddressInventory{
			Inspection: Finding{Status: StatusDetected, Summary: "IPv4 address inventory available"},
			Addresses:  []IPAddress{{Family: "ipv4", Interface: "wlp0s20f3", CIDR: "192.0.2.55/24", Scope: "global"}},
		},
		IPv4Routes: RouteInventory{
			Inspection: Finding{Status: StatusDetected, Summary: "IPv4 route inventory available"},
			Routes: []Route{
				{Status: StatusDetected, Family: "ipv4", Destination: "default", Table: "main", Interface: "wlp0s20f3", Gateway: "192.0.2.1"},
				{Status: StatusDetected, Family: "ipv4", Destination: "192.0.2.0/24", Table: "main", Interface: "wlp0s20f3"},
			},
		},
		IPv4: Finding{Status: StatusDetected, Summary: "IPv4 default route detected"},
		IPv6: Finding{Status: StatusMissing, Summary: "IPv6 default route missing", Detail: "route not found"},
	}
}

func FakeDesktopWithForeignDefaultDNSOwner() Snapshot {
	s := FakeResolvedDesktop()
	s.DNS.ResolvedLinks = append(s.DNS.ResolvedLinks, ResolvedLink{
		Index:            "9",
		Name:             "wg0",
		CurrentScopes:    []string{"DNS"},
		Protocols:        []string{"+DefaultRoute", "+LLMNR", "-mDNS", "-DNSOverTLS", "DNSSEC=no/unsupported"},
		CurrentDNSServer: "198.51.100.53",
		DNSServers:       []string{"198.51.100.53"},
		DNSDomains:       []string{"~."},
	})
	return s
}

func FakeDesktopWithForeignTunLikeInterface() Snapshot {
	s := FakeResolvedDesktop()
	s.TunDevices = append(s.TunDevices, TunDevice{Name: "wg0", Status: StatusDetected, Raw: "9: wg0: <POINTOPOINT,UP> mtu 1420"})
	return s
}

func FakeDesktopWithForeignPolicyRouting() Snapshot {
	s := FakeResolvedDesktop()
	s.PolicyRouting = []PolicyRoutingSignal{{Kind: "rule", Priority: "100", Fwmark: "0xca6c", Table: "51821", Raw: "100: from all fwmark 0xca6c lookup 51821"}}
	return s
}

func FakeDesktopWithActiveNetworkManagerVPN() Snapshot {
	s := FakeResolvedDesktop()
	s.NetworkManager.ActiveConnections = []NetworkManagerConnection{{Name: "Work VPN", UUID: "11111111-2222-3333-4444-555555555555", Type: "vpn", Device: "tun0", State: "activated"}}
	return s
}

func FakeDesktopWithServerRouteViaForeignVPN() Snapshot {
	s := FakeDesktopWithForeignTunLikeInterface()
	s.ServerRoute = Route{Status: StatusDetected, Destination: "example.com", Interface: "wg0", Raw: "203.0.113.10 dev wg0 src 10.64.0.2 uid 1000"}
	return s
}

func FakeDesktopMissingDefaultRoute() Snapshot {
	s := FakeResolvedDesktop()
	s.DefaultIPv4 = Route{Status: StatusMissing, Family: "ipv4", Destination: "default", Detail: "route not found"}
	s.IPv4 = Finding{Status: StatusMissing, Summary: "IPv4 default route missing", Detail: "route not found"}
	s.ServerRoute = Route{Status: StatusUnknown, Destination: "example.com", Detail: "default route missing"}
	return s
}

func FakeDesktopWithServerRouteLoop() Snapshot {
	s := FakeResolvedDesktop()
	s.ServerRoute = Route{Status: StatusDetected, Destination: "example.com", Interface: DefaultTunName, Raw: "203.0.113.10 dev podlaz0 src 10.0.0.2 uid 1000"}
	return s
}

func FakeDesktopWithoutOptionalTools() Snapshot {
	s := FakeResolvedDesktop()
	s.DNS = DNS{Mode: "unknown", Resolved: Finding{Status: StatusMissing, Summary: "resolvectl not found"}}
	s.NetworkManager = NetworkManager{Finding: Finding{Status: StatusMissing, Summary: "nmcli not found"}}
	s.Nftables = Nftables{Availability: Finding{Status: StatusMissing, Summary: "nft not found"}, PodlazTable: Finding{Status: StatusMissing, Summary: "podlaz nftables table not inspected because nft is unavailable"}}
	return s
}

func FakeDesktopWithStalepodlazResources() Snapshot {
	s := FakeResolvedDesktop()
	s.TunDevices = []TunDevice{{Name: DefaultTunName, Status: StatusDetected, Raw: "7: podlaz0: <POINTOPOINT,UP> mtu 1500", Detail: "7: podlaz0: <POINTOPOINT,UP> mtu 1500 tun type tun"}}
	s.Nftables.PodlazTable = Finding{Status: StatusDetected, Summary: "podlaz nftables table exists"}
	s.StaleResources = []StaleResource{{Kind: "tun-device", Name: DefaultTunName, Status: StatusDetected, Detail: "7: podlaz0: <POINTOPOINT,UP> mtu 1500"}, {Kind: "nftables-table", Name: DefaultNFTFamily + " " + DefaultNFTTable, Status: StatusDetected}}
	return s
}
