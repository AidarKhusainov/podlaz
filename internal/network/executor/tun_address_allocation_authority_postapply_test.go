package executor

import (
	"context"
	"net/netip"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"golang.org/x/sys/unix"
)

func TestTunAddressGlobalAllocationAcceptsExactKernelLocalRouteAfterOwnApply(t *testing.T) {
	plan := rollbackIdentityAddressPlanForTest()
	plan.Action = planner.TunAddressActionAssignExclusive
	exec := IPTunAddressExecutor{
		AllocationEvidenceCollector: func(context.Context) (netsnapshot.TunAllocationEvidence, error) {
			return netsnapshot.TunAllocationEvidence{
				IPv4Addresses: []netip.Prefix{netip.MustParsePrefix(plan.CIDR)},
				IPv4Routes: []netsnapshot.TunAllocationRoute{{
					Destination: netip.MustParsePrefix(plan.CIDR),
					Table:       unix.RT_TABLE_LOCAL,
					Type:        unix.RTN_LOCAL,
					LinkIndex:   plan.LinkIndex,
				}},
			}, nil
		},
	}

	if err := exec.verifyGlobalTunAddressAllocation(context.Background(), plan, 1, "after address apply"); err != nil {
		t.Fatalf("own kernel local route must be accepted after exact address apply: %v", err)
	}
}
