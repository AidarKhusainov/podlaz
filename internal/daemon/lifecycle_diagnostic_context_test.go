package daemon

import (
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/doctor"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestLifecycleDiagnosticContextAuthorizesOnlyExactCommittedTUNOwnership(t *testing.T) {
	runtimeDir := t.TempDir()
	tx := txstate.NewTransaction("tx-active", "profile-test", planner.ModeTun, time.Now().UTC())
	tx.State = txstate.TransactionCommitted
	tx.Rollback.TUNAddresses = []txstate.TUNAddressRollback{{
		Family: "ipv4", InterfaceName: "podlaz0", CIDR: planner.DefaultTunIPv4CIDR,
		Scope: "global", LinkIndex: 7, LinkKind: "tun", AppearedAfterCore: true, Owner: netexecutor.OwnerTunAddress,
	}}
	tx.Rollback.NFTables = []txstate.NFTablesRollback{{Family: netsnapshot.DefaultNFTFamily, Table: netsnapshot.DefaultNFTTable, Owner: netexecutor.OwnerFirewall}}
	setLifecycleDiagnosticNFTDesiredState(&tx)
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}

	got := lifecycleDiagnosticContext(runtimeDir, xrayState{Connection: "active", Mode: planner.ModeTun, ProfileID: tx.ProfileID, TransactionID: tx.ID})
	if got.State != doctor.LifecycleActiveTUN || got.TransactionState != txstate.TransactionCommitted {
		t.Fatalf("unexpected lifecycle context: %#v", got)
	}
	if got.Interface != doctor.ManagedResourceExactOwned || got.InterfaceLinkIndex != 7 || got.InterfaceLinkKind != "tun" {
		t.Fatalf("exact transaction-bound TUN identity was not projected: %#v", got)
	}
	if got.NFTTable != doctor.ManagedResourceExactOwned || got.NFTPlan == nil {
		t.Fatalf("full transaction-bound nft composition was not projected: %#v", got)
	}
	if got.NFTPlan.Family != netsnapshot.DefaultNFTFamily || got.NFTPlan.Table != netsnapshot.DefaultNFTTable || len(got.NFTPlan.Chains) != 1 || len(got.NFTPlan.Rules) != 1 {
		t.Fatalf("projected nft composition is incomplete: %#v", got.NFTPlan)
	}
}

func TestLifecycleDiagnosticContextDoesNotAuthorizeNFTFromRollbackTupleAlone(t *testing.T) {
	runtimeDir := t.TempDir()
	tx := txstate.NewTransaction("tx-active", "profile-test", planner.ModeTun, time.Now().UTC())
	tx.State = txstate.TransactionCommitted
	tx.Rollback.NFTables = []txstate.NFTablesRollback{{Family: netsnapshot.DefaultNFTFamily, Table: netsnapshot.DefaultNFTTable, Owner: netexecutor.OwnerFirewall}}
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}

	got := lifecycleDiagnosticContext(runtimeDir, xrayState{Connection: "active", Mode: planner.ModeTun, ProfileID: tx.ProfileID, TransactionID: tx.ID})
	if got.NFTTable != doctor.ManagedResourceUnproven || got.NFTPlan != nil {
		t.Fatalf("family/name rollback metadata alone authorized nft ownership: %#v", got)
	}
}

func TestLifecycleDiagnosticContextFailsClosedWhenActiveTransactionIsMissing(t *testing.T) {
	got := lifecycleDiagnosticContext(t.TempDir(), xrayState{Connection: "active", Mode: planner.ModeTun, TransactionID: "missing"})
	if got.State != doctor.LifecycleActiveTUN || got.TransactionState != "" || got.Interface != doctor.ManagedResourceUnproven || got.NFTTable != doctor.ManagedResourceUnproven {
		t.Fatalf("missing active transaction was not fail-closed: %#v", got)
	}
}

func TestLifecycleDiagnosticContextExpectsManagedResourcesAbsentWhenInactive(t *testing.T) {
	got := lifecycleDiagnosticContext(t.TempDir(), inactiveXrayState())
	if got.State != doctor.LifecycleInactive || got.Interface != doctor.ManagedResourceExpectedAbsent || got.NFTTable != doctor.ManagedResourceExpectedAbsent {
		t.Fatalf("inactive lifecycle context is not explicit: %#v", got)
	}
}

func setLifecycleDiagnosticNFTDesiredState(tx *txstate.Transaction) {
	tx.DesiredPlan.NFT = txstate.NFTPlan{
		Family: netsnapshot.DefaultNFTFamily,
		Table:  netsnapshot.DefaultNFTTable,
		Owner:  netexecutor.OwnerFirewall,
		Chains: []txstate.NFTChainPlan{{
			Name:     planner.FirewallOutputChain,
			Type:     planner.FirewallChainTypeFilter,
			Hook:     planner.FirewallOutputHook,
			Priority: planner.FirewallOutputPriority,
			Policy:   planner.FirewallDefaultChainPolicy,
			Owner:    netexecutor.OwnerFirewall,
			Rules:    []string{`oifname podlaz0 accept owner podlaz:firewall:tun-egress`},
		}},
	}
}
