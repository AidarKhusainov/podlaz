package executor

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestTunAddressGlobalAllocationUsesTypedKernelEvidence(t *testing.T) {
	calls := 0
	exec := IPTunAddressExecutor{
		AllocationEvidenceCollector: func(context.Context) (netsnapshot.TunAllocationEvidence, error) {
			calls++
			return netsnapshot.TunAllocationEvidence{
				IPv4Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")},
				IPv4Routes: []netsnapshot.TunAllocationRoute{
					{Destination: netip.MustParsePrefix("198.18.0.0/15"), Table: 254},
				},
			}, nil
		},
	}
	plan := planner.TunAddressPlan{
		Interface: "podlaz0",
		CIDR:      planner.DefaultTunIPv4CIDR,
		Action:    planner.TunAddressActionAssignExclusive,
	}

	err := exec.verifyGlobalTunAddressAllocation(context.Background(), plan, 0, "before address apply")
	if err == nil || !errors.Is(err, ErrTunAddressConflict) {
		t.Fatalf("foreign route overlap must fail closed with TUN address conflict, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("typed allocation evidence collector calls = %d, want 1", calls)
	}
}

func TestTunAddressGlobalAllocationFailsClosedWhenTypedEvidenceUnavailable(t *testing.T) {
	exec := IPTunAddressExecutor{
		AllocationEvidenceCollector: func(context.Context) (netsnapshot.TunAllocationEvidence, error) {
			return netsnapshot.TunAllocationEvidence{}, errors.New("synthetic rtnetlink failure")
		},
	}
	plan := planner.TunAddressPlan{
		Interface: "podlaz0",
		CIDR:      planner.DefaultTunIPv4CIDR,
		Action:    planner.TunAddressActionAssignExclusive,
	}

	err := exec.verifyGlobalTunAddressAllocation(context.Background(), plan, 0, "before address apply")
	if err == nil || !errors.Is(err, ErrTunAddressConflict) || !strings.Contains(err.Error(), "synthetic rtnetlink failure") {
		t.Fatalf("unavailable typed allocation evidence must fail closed, got %v", err)
	}
}
