package recovery

import (
	"time"

	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestRecoveryRollbackMetadataUsesBoundDesiredAddressOnlyAfterMutationCanStart(t *testing.T) {
	base := txstate.NewTransaction("tx-address-crash", "profile-1", planner.ModeTun, time.Now().UTC())
	base.DesiredPlan.TUNAddress = txstate.TUNAddressDesiredState{
		Family:            "ipv4",
		InterfaceName:     managedInterface,
		CIDR:              planner.DefaultTunIPv4CIDR,
		Scope:             "global",
		LinkIndex:         7,
		LinkKind:          "tun",
		AppearedAfterCore: true,
		Owner:             netexecutor.OwnerTunAddress,
	}

	planned := base
	planned.State = txstate.TransactionPlanned
	if got := recoveryRollbackMetadata(planned); len(got.TUNAddresses) != 0 {
		t.Fatalf("planned intent must not grant address rollback authority: %#v", got.TUNAddresses)
	}

	applying := base
	applying.State = txstate.TransactionApplying
	got := recoveryRollbackMetadata(applying)
	if len(got.TUNAddresses) != 1 || got.TUNAddresses[0].LinkIndex != 7 || got.TUNAddresses[0].CIDR != planner.DefaultTunIPv4CIDR {
		t.Fatalf("applying bound identity must become an inspection-gated address candidate: %#v", got.TUNAddresses)
	}
}

func TestRecoveryRollbackMetadataDoesNotSynthesizeDesiredNetworkOwnership(t *testing.T) {
	tx := txstate.Transaction{
		State: txstate.TransactionPlanned,
		DesiredPlan: txstate.DesiredPlan{
			TUN: txstate.TUNDesiredState{InterfaceName: managedInterface, Owner: "xray:tun-inbound"},
			Routes: []txstate.RoutePlan{
				{Table: planner.TunRoutingTable, CIDR: "default", Dev: managedInterface, Owner: netexecutor.OwnerRoute, Operation: "add"},
			},
			DNS:   txstate.DNSPlan{Backend: planner.DNSBackendSystemdResolved, Link: managedInterface, SearchDomains: []string{"~."}, Owner: netexecutor.OwnerDNS},
			NFT:   txstate.NFTPlan{Family: managedNFTFamily, Table: managedNFTTableName, Owner: netexecutor.OwnerFirewall},
			Steps: []txstate.PlannedStep{{Kind: "policy-rule", Target: "priority 10000 from all lookup podlaz", Owner: netexecutor.OwnerPolicyRule}},
		},
	}

	for _, state := range []txstate.TransactionState{txstate.TransactionPlanned, txstate.TransactionApplying, txstate.TransactionApplied, txstate.TransactionVerifying, txstate.TransactionCommitted} {
		t.Run(string(state), func(t *testing.T) {
			candidate := tx
			candidate.State = state
			got := recoveryRollbackMetadata(candidate)
			if len(got.Routes) != 0 || len(got.PolicyRules) != 0 || len(got.DNS) != 0 || len(got.NFTables) != 0 {
				t.Fatalf("desired intent granted network cleanup authority in %s: %#v", state, got)
			}
		})
	}
}

func TestRecoveryRollbackMetadataRejectsForeignDesiredTargets(t *testing.T) {
	tx := txstate.Transaction{DesiredPlan: txstate.DesiredPlan{
		TUN:    txstate.TUNDesiredState{InterfaceName: "wg0", Owner: netexecutor.OwnerTunDevice},
		Routes: []txstate.RoutePlan{{Table: "foreign", CIDR: "default", Dev: "wg0", Owner: "foreign", Operation: "add"}},
		DNS:    txstate.DNSPlan{Backend: planner.DNSBackendSystemdResolved, Link: "wg0", Owner: txstate.TransactionOwner},
		NFT:    txstate.NFTPlan{Family: "inet", Table: "foreign", Owner: netexecutor.OwnerFirewall},
		Steps:  []txstate.PlannedStep{{Kind: "policy-rule", Target: "priority 10000 from all lookup foreign", Owner: "foreign"}},
	}}

	got := recoveryRollbackMetadata(tx)
	if got.Available() {
		t.Fatalf("foreign or unowned desired targets must not become rollback metadata: %#v", got)
	}
}
