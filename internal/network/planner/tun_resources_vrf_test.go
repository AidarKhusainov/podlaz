package planner

import (
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestAllocateTunResourcesAvoidsReservedVRFTableAndTablelessRulePriority(t *testing.T) {
	evidence := snapshot.TunAllocationEvidence{
		ReservedRoutingTables: []uint32{TunRoutingTableID},
		IPv4PolicyRules: []snapshot.TunAllocationRule{
			{Priority: 100, Table: 0},
		},
	}

	allocation, err := AllocateTunResources(evidence)
	if err != nil {
		t.Fatalf("AllocateTunResources() error = %v", err)
	}
	if allocation.RoutingTableID == TunRoutingTableID {
		t.Fatalf("allocator reused VRF-reserved routing table: %#v", allocation)
	}
	if allocation.ServerRulePriority != 98 || allocation.TunnelRulePriority != 99 {
		t.Fatalf("allocator did not reserve table-less rule priority 100: %#v", allocation)
	}
}
