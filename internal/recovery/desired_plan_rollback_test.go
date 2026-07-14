package recovery

import (
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestRecoveryRollbackMetadataSynthesizesOnlyReservedPodlazState(t *testing.T) {
	tx := txstate.Transaction{
		DesiredPlan: txstate.DesiredPlan{
			TUN: txstate.TUNDesiredState{InterfaceName: managedInterface, Owner: "xray:tun-inbound"},
			Routes: []txstate.RoutePlan{
				{Table: planner.TunRoutingTable, CIDR: "default", Dev: managedInterface, Owner: netexecutor.OwnerRoute, Operation: "add"},
				{Table: planner.MainRoutingTable, CIDR: "203.0.113.10/32", Via: "192.0.2.1", Dev: "eth0", Owner: netexecutor.OwnerRoute, Operation: "add"},
			},
			DNS: txstate.DNSPlan{Backend: planner.DNSBackendSystemdResolved, Link: managedInterface, SearchDomains: []string{"~."}, Owner: txstate.TransactionOwner},
			NFT: txstate.NFTPlan{Family: managedNFTFamily, Table: managedNFTTableName, Owner: netexecutor.OwnerFirewall},
			Steps: []txstate.PlannedStep{
				{Kind: "policy-rule", Target: "priority 9999 to 203.0.113.10/32 lookup main", Owner: netexecutor.OwnerPolicyRule},
				{Kind: "policy-rule", Target: "priority 10000 from all lookup podlaz", Owner: netexecutor.OwnerPolicyRule},
			},
		},
		Rollback: txstate.RollbackMetadata{
			GeneratedConfigs: []txstate.GeneratedConfigRollback{{Path: "/run/podlaz/generated/xray.json", Owner: txstate.TransactionOwner}},
			ChildProcesses:   []txstate.ChildProcessRollback{{PID: 1234, Label: "xray", Owner: txstate.TransactionOwner}},
		},
	}

	got := recoveryRollbackMetadata(tx)
	if len(got.TUN) != 0 {
		t.Fatalf("Xray-owned TUN link must not be synthesized as daemon-owned rollback: %#v", got.TUN)
	}
	if len(got.Routes) != 1 || got.Routes[0].Table != planner.TunRoutingTable {
		t.Fatalf("only the reserved podlaz route may be synthesized: %#v", got.Routes)
	}
	if len(got.PolicyRules) != 1 || got.PolicyRules[0].Priority != planner.TunRulePriority || got.PolicyRules[0].Table != planner.TunRoutingTable {
		t.Fatalf("only the reserved podlaz policy rule may be synthesized: %#v", got.PolicyRules)
	}
	if len(got.DNS) != 1 || len(got.NFTables) != 1 {
		t.Fatalf("expected guarded DNS and nftables recovery metadata: %#v", got)
	}
	if got.DNS[0].Backend != normalizedSystemdResolvedBackend || got.DNS[0].Link != managedInterface {
		t.Fatalf("unexpected normalized DNS rollback metadata: %#v", got.DNS)
	}
	if len(got.GeneratedConfigs) != 1 || len(got.ChildProcesses) != 1 {
		t.Fatalf("existing process/config rollback metadata must be preserved: %#v", got)
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
