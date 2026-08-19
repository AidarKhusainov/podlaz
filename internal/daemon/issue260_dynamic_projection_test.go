package daemon

import (
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTunPlanFromTransactionAcceptsExactAllocatedTunAddress(t *testing.T) {
	const allocatedCIDR = "198.18.0.2/32"
	tx := txstate.NewTransaction("issue260-dynamic-address", "example-profile", planner.ModeTun, fixedClock()())
	tx.DesiredPlan.TUNAddress = txstate.TUNAddressDesiredState{
		Family:            "ipv4",
		InterfaceName:     "podlaz0",
		CIDR:              allocatedCIDR,
		Scope:             "global",
		LinkIndex:         17,
		LinkKind:          "tun",
		AppearedAfterCore: true,
		Owner:             netexecutor.OwnerTunAddress,
	}
	tx.Rollback.TUNAddresses = []txstate.TUNAddressRollback{{
		Family:            "ipv4",
		InterfaceName:     "podlaz0",
		CIDR:              allocatedCIDR,
		Scope:             "global",
		LinkIndex:         17,
		LinkKind:          "tun",
		AppearedAfterCore: true,
		Owner:             netexecutor.OwnerTunAddress,
	}}

	got := tunPlanFromTransaction(tx).TunAddress
	if got.Action != planner.TunAddressActionAssign || got.CIDR != allocatedCIDR || got.LinkIndex != 17 {
		t.Fatalf("unexpected dynamic rollback projection: %#v", got)
	}
}

func TestTunPlanFromTransactionRejectsAllocatedAddressThatDoesNotMatchDesiredIdentity(t *testing.T) {
	tx := txstate.NewTransaction("issue260-dynamic-address-mismatch", "example-profile", planner.ModeTun, fixedClock()())
	tx.DesiredPlan.TUNAddress = txstate.TUNAddressDesiredState{
		Family:            "ipv4",
		InterfaceName:     "podlaz0",
		CIDR:              "198.18.0.2/32",
		Scope:             "global",
		LinkIndex:         17,
		LinkKind:          "tun",
		AppearedAfterCore: true,
		Owner:             netexecutor.OwnerTunAddress,
	}
	tx.Rollback.TUNAddresses = []txstate.TUNAddressRollback{{
		Family:            "ipv4",
		InterfaceName:     "podlaz0",
		CIDR:              "198.18.0.3/32",
		Scope:             "global",
		LinkIndex:         17,
		LinkKind:          "tun",
		AppearedAfterCore: true,
		Owner:             netexecutor.OwnerTunAddress,
	}}

	got := tunPlanFromTransaction(tx)
	if got.TunDevice.Action != "invalid-rollback-projection" {
		t.Fatalf("mismatched dynamic address must fail closed, got %#v", got)
	}
}
