package snapshot

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	paths    map[string]string
	commands map[string]CommandResult
}

func (f fakeRunner) LookPath(file string) (string, error) {
	path, ok := f.paths[file]
	if !ok {
		return "", errors.New("not found")
	}
	return path, nil
}

func (f fakeRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	key := name + " " + strings.Join(args, " ")
	result, ok := f.commands[key]
	if !ok {
		return CommandResult{ExitCode: 1, Stderr: "unexpected command " + key}, errors.New("unexpected command")
	}
	if result.ExitCode != 0 {
		return result, errors.New("command failed")
	}
	return result, nil
}

func TestCollectWithRunnerBuildsReadOnlySnapshot(t *testing.T) {
	runner := fakeRunner{
		paths: map[string]string{
			"ip":         "/usr/sbin/ip",
			"resolvectl": "/usr/bin/resolvectl",
			"nmcli":      "/usr/bin/nmcli",
			"nft":        "/usr/sbin/nft",
		},
		commands: map[string]CommandResult{
			"/usr/sbin/ip -4 route show default": {Stdout: "default via 192.0.2.1 dev wlp0s20f3 proto dhcp metric 600"},
			"/usr/sbin/ip -6 route show default": {ExitCode: 1, Stderr: "RTNETLINK answers: Network is unreachable"},
			"/usr/sbin/ip route get 203.0.113.10": {Stdout: "203.0.113.10 via 192.0.2.1 dev wlp0s20f3 src 192.0.2.55 uid 1000"},
			"/usr/sbin/ip link show dev podlaz0": {ExitCode: 1, Stderr: "Device \"podlaz0\" does not exist."},
			"/usr/bin/resolvectl status --no-pager": {Stdout: resolvedStatusWithDesktopLinkForTest},
			"env LC_ALL=C /usr/bin/nmcli -t -e yes -f RUNNING,STATE general": {Stdout: "running:connected"},
			"env LC_ALL=C /usr/bin/nmcli -t -e yes -f NAME,UUID,TYPE,DEVICE,STATE connection show --active": {Stdout: "Example:11111111-2222-3333-4444-555555555555:802-11-wireless:wlp0s20f3:activated"},
			"/usr/sbin/nft list tables": {Stdout: "table inet filter"},
		},
	}

	s := CollectWithRunner(context.Background(), runner, Options{Server: "203.0.113.10", OS: "linux"})

	if s.DefaultIPv4.Status != StatusDetected || s.DefaultIPv4.Interface != "wlp0s20f3" || s.DefaultIPv4.Gateway != "192.0.2.1" {
		t.Fatalf("unexpected IPv4 default route: %#v", s.DefaultIPv4)
	}
	if s.DefaultIPv6.Status != StatusUnknown {
		t.Fatalf("expected unknown IPv6 route after command failure, got %#v", s.DefaultIPv6)
	}
	if s.ServerRoute.Status != StatusDetected || s.ServerRoute.Interface != "wlp0s20f3" {
		t.Fatalf("unexpected server route: %#v", s.ServerRoute)
	}
	if s.DNS.Mode != "systemd-resolved" || s.NetworkManager.State != "connected" {
		t.Fatalf("unexpected DNS/NM snapshot: %#v %#v", s.DNS, s.NetworkManager)
	}
	if s.NetworkManager.ActiveConnectionsInspection.Status != StatusDetected || len(s.NetworkManager.ActiveConnections) != 1 || s.NetworkManager.ActiveConnections[0].Device != "wlp0s20f3" {
		t.Fatalf("expected authoritative active NetworkManager inventory, got %#v", s.NetworkManager)
	}
	if len(s.DNS.ResolvedLinks) != 1 || s.DNS.ResolvedLinks[0].Name != "wlp0s20f3" || !containsValue(s.DNS.ResolvedLinks[0].CurrentScopes, "DNS") {
		t.Fatalf("expected parsed resolved link DNS scope, got %#v", s.DNS.ResolvedLinks)
	}
	if s.Nftables.Availability.Status != StatusDetected || s.Nftables.PodlazTable.Status != StatusMissing {
		t.Fatalf("unexpected nftables snapshot: %#v", s.Nftables)
	}
	if len(s.StaleResources) != 0 {
		t.Fatalf("expected no stale resources, got %#v", s.StaleResources)
	}
}

func TestParseResolvedLinksDetectsRouteOnlyDefaultDNSScope(t *testing.T) {
	links := ParseResolvedLinks(`Global
       Protocols: +LLMNR +mDNS -DNSOverTLS DNSSEC=no/unsupported

Link 3 (wlp0s20f3)
    Current Scopes: DNS
         Protocols: +DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
Current DNS Server: 192.0.2.53
       DNS Servers: 192.0.2.53
        DNS Domain: lan.example.invalid

Link 9 (wg0)
    Current Scopes: DNS
         Protocols: +DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
Current DNS Server: 198.51.100.53
       DNS Servers: 198.51.100.53
                    198.51.100.54
        DNS Domain: ~.`)

	if len(links) != 2 {
		t.Fatalf("expected two parsed links, got %#v", links)
	}
	foreign := links[1]
	if foreign.Name != "wg0" || foreign.CurrentDNSServer != "198.51.100.53" || !containsValue(foreign.DNSDomains, "~.") || !containsValue(foreign.DNSServers, "198.51.100.54") {
		t.Fatalf("unexpected foreign DNS owner parse result: %#v", foreign)
	}
}

func TestCollectWithRunnerResolvesHostnameBeforeServerRouteLookup(t *testing.T) {
	runner := fakeRunner{
		paths: map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]CommandResult{
			"/usr/sbin/ip -4 route show default":  {Stdout: "default via 192.0.2.1 dev eth0"},
			"/usr/sbin/ip -6 route show default":  {ExitCode: 1, Stderr: "RTNETLINK answers: Network is unreachable"},
			"/usr/sbin/ip route get 203.0.113.10": {Stdout: "203.0.113.10 via 192.0.2.1 dev eth0"},
			"/usr/sbin/ip link show dev podlaz0":  {ExitCode: 1, Stderr: "Device \"podlaz0\" does not exist."},
		},
	}
	resolved := false
	resolver := func(ctx context.Context, host string) ([]string, error) {
		resolved = true
		if host != "example.com" {
			t.Fatalf("unexpected host: %q", host)
		}
		return []string{"2001:db8::10", "203.0.113.10"}, nil
	}

	s := CollectWithRunner(context.Background(), runner, Options{Server: "example.com", OS: "linux", ResolveHost: resolver})

	if !resolved {
		t.Fatal("expected hostname resolver to be called")
	}
	if s.ServerRoute.Status != StatusDetected || s.ServerRoute.Interface != "eth0" {
		t.Fatalf("unexpected server route: %#v", s.ServerRoute)
	}
	if !strings.Contains(s.ServerRoute.Detail, "example.com resolved to 203.0.113.10") {
		t.Fatalf("expected resolved hostname detail, got %#v", s.ServerRoute)
	}
}

func TestCollectWithRunnerDoesNotResolveIPLiteralServer(t *testing.T) {
	runner := fakeRunner{
		paths: map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]CommandResult{
			"/usr/sbin/ip -4 route show default":  {Stdout: "default via 192.0.2.1 dev eth0"},
			"/usr/sbin/ip -6 route show default":  {ExitCode: 1, Stderr: "RTNETLINK answers: Network is unreachable"},
			"/usr/sbin/ip route get 203.0.113.10": {Stdout: "203.0.113.10 via 192.0.2.1 dev eth0"},
			"/usr/sbin/ip link show dev podlaz0":  {ExitCode: 1, Stderr: "Device \"podlaz0\" does not exist."},
		},
	}
	resolver := func(ctx context.Context, host string) ([]string, error) {
		t.Fatalf("resolver should not be called for IP literal %q", host)
		return nil, nil
	}

	s := CollectWithRunner(context.Background(), runner, Options{Server: "203.0.113.10", OS: "linux", ResolveHost: resolver})

	if s.ServerRoute.Status != StatusDetected {
		t.Fatalf("expected detected server route, got %#v", s.ServerRoute)
	}
}

func TestCollectWithRunnerReportsHostnameResolutionFailure(t *testing.T) {
	runner := fakeRunner{
		paths: map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]CommandResult{
			"/usr/sbin/ip -4 route show default": {Stdout: "default via 192.0.2.1 dev eth0"},
			"/usr/sbin/ip -6 route show default": {ExitCode: 1, Stderr: "RTNETLINK answers: Network is unreachable"},
			"/usr/sbin/ip link show dev podlaz0": {ExitCode: 1, Stderr: "Device \"podlaz0\" does not exist."},
		},
	}
	resolver := func(ctx context.Context, host string) ([]string, error) {
		return nil, errors.New("dns timeout")
	}

	s := CollectWithRunner(context.Background(), runner, Options{Server: "example.com", OS: "linux", ResolveHost: resolver})

	if s.ServerRoute.Status != StatusUnknown {
		t.Fatalf("expected unknown server route on DNS failure, got %#v", s.ServerRoute)
	}
	if !strings.Contains(s.ServerRoute.Detail, "resolve server hostname") || !strings.Contains(s.ServerRoute.Detail, "dns timeout") {
		t.Fatalf("expected DNS failure detail, got %#v", s.ServerRoute)
	}
}

func TestCollectWithRunnerReportsHostnameResolutionTimeout(t *testing.T) {
	runner := fakeRunner{
		paths: map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]CommandResult{
			"/usr/sbin/ip -4 route show default": {Stdout: "default via 192.0.2.1 dev eth0"},
			"/usr/sbin/ip -6 route show default": {ExitCode: 1, Stderr: "RTNETLINK answers: Network is unreachable"},
			"/usr/sbin/ip link show dev podlaz0": {ExitCode: 1, Stderr: "Device \"podlaz0\" does not exist."},
		},
	}
	resolver := func(ctx context.Context, host string) ([]string, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	s := CollectWithRunner(context.Background(), runner, Options{Server: "example.com", OS: "linux", ResolveHost: resolver, ResolveHostTimeout: time.Nanosecond})

	if s.ServerRoute.Status != StatusUnknown {
		t.Fatalf("expected unknown server route on DNS timeout, got %#v", s.ServerRoute)
	}
	if !strings.Contains(s.ServerRoute.Detail, "context deadline exceeded") {
		t.Fatalf("expected timeout detail, got %#v", s.ServerRoute)
	}
}

func TestCollectWithRunnerDegradesWhenOptionalToolsAreMissing(t *testing.T) {
	runner := fakeRunner{
		paths: map[string]string{"ip": "/usr/sbin/ip"},
		commands: map[string]CommandResult{
			"/usr/sbin/ip -4 route show default":  {Stdout: "default via 192.0.2.1 dev eth0"},
			"/usr/sbin/ip -6 route show default":  {},
			"/usr/sbin/ip route get 203.0.113.10": {Stdout: "203.0.113.10 via 192.0.2.1 dev eth0"},
			"/usr/sbin/ip link show dev podlaz0":  {ExitCode: 1, Stderr: "Device \"podlaz0\" does not exist."},
		},
	}
	resolver := func(ctx context.Context, host string) ([]string, error) {
		return []string{"203.0.113.10"}, nil
	}

	s := CollectWithRunner(context.Background(), runner, Options{Server: "example.com", OS: "linux", ResolveHost: resolver})

	if s.DNS.Resolved.Status != StatusMissing || s.NetworkManager.Finding.Status != StatusMissing || s.Nftables.Availability.Status != StatusMissing {
		t.Fatalf("expected missing optional tool states, got DNS=%#v NM=%#v nft=%#v", s.DNS, s.NetworkManager, s.Nftables)
	}
	if s.DefaultIPv4.Status != StatusDetected {
		t.Fatalf("expected IPv4 route to remain detected, got %#v", s.DefaultIPv4)
	}
}

func TestFakeSnapshotsCoverCommonTopologies(t *testing.T) {
	fakes := []Snapshot{
		FakeResolvedDesktop(),
		FakeDesktopWithoutOptionalTools(),
		FakeDesktopWithStalepodlazResources(),
	}
	for _, s := range fakes {
		if s.OS != "linux" {
			t.Fatalf("fake snapshot should model linux, got %q", s.OS)
		}
		if s.DefaultIPv4.Status == "" || reflect.DeepEqual(s.DNS, DNS{}) || len(s.TunDevices) == 0 {
			t.Fatalf("fake snapshot is incomplete: %#v", s)
		}
	}
}

const resolvedStatusWithDesktopLinkForTest = `Global
       Protocols: +LLMNR +mDNS -DNSOverTLS DNSSEC=no/unsupported

Link 3 (wlp0s20f3)
    Current Scopes: DNS
         Protocols: +DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
Current DNS Server: 192.0.2.53
       DNS Servers: 192.0.2.53
        DNS Domain: lan.example.invalid`

func containsValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestParseIPv4AddressInventoryPreservesExactLinkCIDRAndScope(t *testing.T) {
	inventory, err := ParseIPv4Addresses(`2: wlan0    inet 192.0.2.55/24 brd 192.0.2.255 scope global dynamic wlan0\       valid_lft 86399sec preferred_lft 86399sec
7: podlaz0  inet 198.51.100.1/32 scope link podlaz0\       valid_lft forever preferred_lft forever`)
	if err != nil {
		t.Fatalf("parse IPv4 addresses: %v", err)
	}
	if len(inventory) != 2 {
		t.Fatalf("expected two addresses, got %#v", inventory)
	}
	if inventory[1].Interface != "podlaz0" || inventory[1].CIDR != "198.51.100.1/32" || inventory[1].Scope != "link" {
		t.Fatalf("unexpected TUN address parse: %#v", inventory[1])
	}
}

func TestParseIPv4RouteInventoryPreservesTableAndDestination(t *testing.T) {
	routes, err := ParseIPv4Routes(`default via 192.0.2.1 dev wlan0 table main proto dhcp
198.18.0.0/15 dev eth1 table 100 scope link
local 192.0.2.55 dev wlan0 table local scope host`)
	if err != nil {
		t.Fatalf("parse IPv4 routes: %v", err)
	}
	if len(routes) != 3 {
		t.Fatalf("expected three routes, got %#v", routes)
	}
	if routes[1].Destination != "198.18.0.0/15" || routes[1].Interface != "eth1" || routes[1].Table != "100" {
		t.Fatalf("unexpected route parse: %#v", routes[1])
	}
}

func TestParseIPv4InventoryRejectsMalformedOperationalOutput(t *testing.T) {
	if _, err := ParseIPv4Addresses("not-an-ip-address-line"); err == nil {
		t.Fatal("expected malformed address inventory to fail closed")
	}
	if _, err := ParseIPv4Routes("unparseable route output"); err == nil {
		t.Fatal("expected malformed route inventory to fail closed")
	}
}
