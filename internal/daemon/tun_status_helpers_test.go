package daemon

import (
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTunPlanFromTransactionReconstructsExactOwnedAddressFromRollbackMetadata(t *testing.T) {
	tx := txstate.NewTransaction("tun-status-address", "example-profile", planner.ModeTun, fixedClock()())
	tx.DesiredPlan.TUNAddress = txstate.TUNAddressDesiredState{
		Family:            "ipv4",
		InterfaceName:     "podlaz0",
		CIDR:              planner.DefaultTunIPv4CIDR,
		Scope:             "global",
		LinkIndex:         7,
		LinkKind:          "tun",
		AppearedAfterCore: true,
		Owner:             netexecutor.OwnerTunAddress,
	}
	tx.Rollback.TUNAddresses = []txstate.TUNAddressRollback{{
		Family:            "ipv4",
		InterfaceName:     "podlaz0",
		CIDR:              planner.DefaultTunIPv4CIDR,
		Scope:             "global",
		LinkIndex:         7,
		LinkKind:          "tun",
		AppearedAfterCore: true,
		Owner:             netexecutor.OwnerTunAddress,
	}}

	got := tunPlanFromTransaction(tx).TunAddress
	if got.Action != planner.TunAddressActionAssign || got.Interface != "podlaz0" || got.CIDR != planner.DefaultTunIPv4CIDR {
		t.Fatalf("unexpected reconstructed address plan: %#v", got)
	}
	if got.LinkIndex != 7 || got.LinkKind != "tun" || !got.AppearedAfterCore {
		t.Fatalf("missing exact link identity: %#v", got)
	}
	if got.Owner != netexecutor.OwnerTunAddress || got.RollbackKey != "podlaz0/"+planner.DefaultTunIPv4CIDR {
		t.Fatalf("missing ownership identity: %#v", got)
	}
	if !got.AllowOwnedExisting || got.AllowMissingLink {
		t.Fatalf("active disconnect must verify owned existing address without guessing missing-link success: %#v", got)
	}
}

func TestTunPlanFromTransactionDoesNotGrantAddressRollbackFromDesiredIntentAlone(t *testing.T) {
	tx := txstate.NewTransaction("tun-status-address-intent", "example-profile", planner.ModeTun, fixedClock()())
	tx.DesiredPlan.TUNAddress = txstate.TUNAddressDesiredState{
		Family:            "ipv4",
		InterfaceName:     "podlaz0",
		CIDR:              planner.DefaultTunIPv4CIDR,
		Scope:             "global",
		LinkIndex:         7,
		LinkKind:          "tun",
		AppearedAfterCore: true,
		Owner:             netexecutor.OwnerTunAddress,
	}

	if got := tunPlanFromTransaction(tx).TunAddress; got != (planner.TunAddressPlan{}) {
		t.Fatalf("desired intent must not grant address mutation authority: %#v", got)
	}
}
