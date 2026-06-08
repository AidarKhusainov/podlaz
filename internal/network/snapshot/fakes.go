package snapshot

// FakeResolvedDesktop returns a common Linux desktop topology with systemd-resolved,
// NetworkManager, nftables, and no stale TunWarden-owned resources.
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
		DefaultIPv6: Route{
			Status:      StatusMissing,
			Family:      "ipv6",
			Destination: "default",
			Detail:      "route not found",
		},
		ServerRoute: Route{
			Status:      StatusDetected,
			Destination: "example.com",
			Interface:   "wlp0s20f3",
			Gateway:     "192.0.2.1",
			Raw:         "203.0.113.10 via 192.0.2.1 dev wlp0s20f3 src 192.0.2.55 uid 1000",
		},
		DNS: DNS{Mode: "systemd-resolved", Resolved: Finding{Status: StatusDetected, Summary: "systemd-resolved status available", Detail: "Global"}},
		NetworkManager: NetworkManager{
			Finding: Finding{Status: StatusDetected, Summary: "NetworkManager state available", Detail: "running:connected"},
			State:   "connected",
		},
		Nftables: Nftables{
			Availability:   Finding{Status: StatusDetected, Summary: "nftables table listing available"},
			TunWardenTable: Finding{Status: StatusMissing, Summary: "TunWarden nftables table not found"},
		},
		TunDevices: []TunDevice{{Name: DefaultTunName, Status: StatusMissing, Detail: "device not found"}},
		IPv4:       Finding{Status: StatusDetected, Summary: "IPv4 default route detected"},
		IPv6:       Finding{Status: StatusMissing, Summary: "IPv6 default route missing", Detail: "route not found"},
	}
}

// FakeDesktopWithoutOptionalTools returns a desktop topology where optional tools
// used by TUN planning are unavailable but snapshot collection still succeeds.
func FakeDesktopWithoutOptionalTools() Snapshot {
	s := FakeResolvedDesktop()
	s.DNS = DNS{Mode: "unknown", Resolved: Finding{Status: StatusMissing, Summary: "resolvectl not found"}}
	s.NetworkManager = NetworkManager{Finding: Finding{Status: StatusMissing, Summary: "nmcli not found"}}
	s.Nftables = Nftables{
		Availability:   Finding{Status: StatusMissing, Summary: "nft not found"},
		TunWardenTable: Finding{Status: StatusMissing, Summary: "TunWarden nftables table not inspected because nft is unavailable"},
	}
	return s
}

// FakeDesktopWithStaleTunWardenResources returns a topology with old TunWarden-owned
// resources that future recover/connect flows must handle before mutation.
func FakeDesktopWithStaleTunWardenResources() Snapshot {
	s := FakeResolvedDesktop()
	s.TunDevices = []TunDevice{{Name: DefaultTunName, Status: StatusDetected, Raw: "7: tunwarden0: <POINTOPOINT,UP> mtu 1500"}}
	s.Nftables.TunWardenTable = Finding{Status: StatusDetected, Summary: "TunWarden nftables table exists"}
	s.StaleResources = []StaleResource{
		{Kind: "tun-device", Name: DefaultTunName, Status: StatusDetected, Detail: "7: tunwarden0: <POINTOPOINT,UP> mtu 1500"},
		{Kind: "nftables-table", Name: DefaultNFTFamily + " " + DefaultNFTTable, Status: StatusDetected},
	}
	return s
}
