//go:build linux

package snapshot

import (
	"testing"

	"github.com/vishvananda/netlink"
)

func TestTunAllocationEvidenceFromNetlinkReservesVRFTableAndKeepsTablelessRulePriority(t *testing.T) {
	links := []netlink.Link{
		&netlink.Vrf{LinkAttrs: netlink.LinkAttrs{Name: "vrf-test"}, Table: 51820},
	}
	rules := []netlink.Rule{
		{Priority: 1000, Table: 0, Family: netlink.FAMILY_V4},
	}

	evidence, err := tunAllocationEvidenceFromNetlink(nil, nil, rules, links)
	if err != nil {
		t.Fatalf("tunAllocationEvidenceFromNetlink() error = %v", err)
	}
	if len(evidence.ReservedRoutingTables) != 1 || evidence.ReservedRoutingTables[0] != 51820 {
		t.Fatalf("VRF routing table was not reserved: %#v", evidence.ReservedRoutingTables)
	}
	if len(evidence.IPv4PolicyRules) != 1 || evidence.IPv4PolicyRules[0].Priority != 1000 || evidence.IPv4PolicyRules[0].Table != 0 {
		t.Fatalf("table-less l3mdev-style rule priority was not preserved: %#v", evidence.IPv4PolicyRules)
	}
}
