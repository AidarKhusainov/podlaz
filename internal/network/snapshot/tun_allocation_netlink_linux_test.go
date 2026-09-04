//go:build linux

package snapshot

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestTunAllocationEvidenceFromNetlinkPreservesKernelIdentities(t *testing.T) {
	addresses := []netlink.Addr{
		{IPNet: &net.IPNet{IP: net.ParseIP("192.0.2.10"), Mask: net.CIDRMask(24, 32)}},
		{IPNet: &net.IPNet{IP: net.ParseIP("172.17.0.1"), Mask: net.CIDRMask(16, 32)}},
	}
	routes := []netlink.Route{
		{Dst: &net.IPNet{IP: net.ParseIP("127.0.0.0"), Mask: net.CIDRMask(8, 32)}, Table: unix.RT_TABLE_LOCAL, Type: unix.RTN_LOCAL},
		{Dst: &net.IPNet{IP: net.ParseIP("172.17.0.0"), Mask: net.CIDRMask(16, 32)}, Table: unix.RT_TABLE_MAIN, Type: unix.RTN_UNICAST},
		{Dst: nil, Table: unix.RT_TABLE_MAIN, Type: unix.RTN_UNICAST},
	}
	rules := []netlink.Rule{
		{Priority: 0, Table: unix.RT_TABLE_LOCAL, Family: netlink.FAMILY_V4},
		{Priority: 100, Table: 60000, Family: netlink.FAMILY_V4},
		{Priority: 32766, Table: unix.RT_TABLE_MAIN, Family: netlink.FAMILY_V4},
		{Priority: 32767, Table: unix.RT_TABLE_DEFAULT, Family: netlink.FAMILY_V4},
	}

	evidence, err := tunAllocationEvidenceFromNetlink(addresses, routes, rules)
	if err != nil {
		t.Fatalf("tunAllocationEvidenceFromNetlink() error = %v", err)
	}
	if len(evidence.IPv4Addresses) != 2 {
		t.Fatalf("IPv4 addresses = %#v", evidence.IPv4Addresses)
	}
	if evidence.IPv4Addresses[0] != netip.MustParsePrefix("192.0.2.10/24") {
		t.Fatalf("host prefix lost during conversion: %s", evidence.IPv4Addresses[0])
	}
	if len(evidence.IPv4Routes) != 3 {
		t.Fatalf("IPv4 routes = %#v", evidence.IPv4Routes)
	}
	if evidence.IPv4Routes[0].Destination != netip.MustParsePrefix("127.0.0.0/8") || evidence.IPv4Routes[0].Table != 255 {
		t.Fatalf("local route identity lost: %#v", evidence.IPv4Routes[0])
	}
	if !evidence.IPv4Routes[2].Default || evidence.IPv4Routes[2].Destination.IsValid() {
		t.Fatalf("default route must not invent a destination: %#v", evidence.IPv4Routes[2])
	}
	if len(evidence.IPv4PolicyRules) != 1 || evidence.IPv4PolicyRules[0] != (TunAllocationRule{Priority: 100, Table: 60000}) {
		t.Fatalf("unexpected policy-rule evidence: %#v", evidence.IPv4PolicyRules)
	}
}

func TestTunAllocationEvidenceFromNetlinkRejectsUnspecifiedRouteTable(t *testing.T) {
	_, err := tunAllocationEvidenceFromNetlink(nil, []netlink.Route{{Dst: nil, Table: unix.RT_TABLE_UNSPEC}}, nil)
	if err == nil {
		t.Fatal("unspecified route table must not become allocation authority")
	}
}

func TestCollectTunAllocationEvidenceReadsCurrentLinuxNamespaceWithoutMutation(t *testing.T) {
	evidence, err := CollectTunAllocationEvidence(context.Background())
	if err != nil {
		t.Fatalf("CollectTunAllocationEvidence() error = %v", err)
	}
	if len(evidence.IPv4Addresses) == 0 {
		t.Fatal("expected at least one IPv4 address from the current Linux namespace")
	}
	if len(evidence.IPv4Routes) == 0 {
		t.Fatal("expected at least one IPv4 route from the current Linux namespace")
	}
	for _, prefix := range evidence.IPv4Addresses {
		if !prefix.IsValid() || !prefix.Addr().Is4() {
			t.Fatalf("invalid IPv4 address evidence: %s", prefix)
		}
	}
	for _, route := range evidence.IPv4Routes {
		if route.Table == 0 {
			t.Fatalf("route has unspecified table: %#v", route)
		}
		if !route.Default && (!route.Destination.IsValid() || !route.Destination.Addr().Is4()) {
			t.Fatalf("invalid IPv4 route evidence: %#v", route)
		}
	}
}
