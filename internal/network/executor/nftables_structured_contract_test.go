package executor

import (
	"context"
	"reflect"
	"testing"
)

func TestNftablesExecutorVerifyUsesStructuredSemanticEvidence(t *testing.T) {
	plan := firewallPlanForTest()
	runner := &privacyEnvelopeRecordingRunner{tableJSON: nftablesJSONForTest()}

	if err := (NftablesExecutor{Runner: runner}).Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify transaction-owned nftables table: %v", err)
	}
	want := [][]string{{"nft", "-j", "list", "table", "inet", "podlaz"}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("verify commands=%#v, want structured observation %#v", runner.commands, want)
	}
}

func TestVerifyNftablesTableOutputUsesStructuredSemanticEvidence(t *testing.T) {
	plan := firewallPlanForTest()
	if err := VerifyNftablesTableOutput(plan, nftablesJSONForTest()); err != nil {
		t.Fatalf("verify structured transaction-owned nftables output: %v", err)
	}
}

func TestNftablesExecutorRollbackUsesFreshGenerationGuardedVerification(t *testing.T) {
	plan := firewallPlanForTest()
	runner := &privacyEnvelopeRecordingRunner{
		presenceJSON: nftTablesPresenceJSONForTest("inet", "podlaz", 21),
		tableJSON:    nftablesJSONForTest(),
	}
	backend := coherentTestMutationBackend(80)
	removeCalls := 0
	backend.removeTable = func(_ context.Context, target nftMutationTarget) error {
		removeCalls++
		if target.Family != "inet" || target.Table != "podlaz" || target.Handle != 21 || target.Generation != 80 {
			t.Fatalf("unexpected guarded rollback target: %#v", target)
		}
		return nil
	}

	if err := (NftablesExecutor{Runner: runner, mutation: backend}).Rollback(context.Background(), plan); err != nil {
		t.Fatalf("generation-guarded rollback: %v", err)
	}
	if removeCalls != 1 {
		t.Fatalf("guarded rollback calls=%d, want 1", removeCalls)
	}
	want := [][]string{
		{"nft", "-j", "list", "tables"},
		{"nft", "-j", "list", "table", "inet", "podlaz"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("rollback observation commands=%#v, want %#v", runner.commands, want)
	}
}

func nftablesJSONForTest() string {
	return `{"nftables":[
{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},
{"table":{"family":"inet","name":"podlaz","handle":21}},
{"chain":{"family":"inet","table":"podlaz","name":"output","handle":1,"type":"filter","hook":"output","prio":0,"policy":"accept"}},
{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":1,"expr":[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"203.0.113.10"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:firewall:server-bypass"}},
{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":2,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"lo"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:firewall:loopback"}},
{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":3,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"podlaz0"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:firewall:tun-egress"}},
{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":4,"expr":[{"match":{"op":"!=","left":{"meta":{"key":"oifname"}},"right":"podlaz0"}},{"counter":{"packets":0,"bytes":0}},{"reject":{"type":"icmpx","expr":"port-unreachable"}}],"comment":"podlaz:firewall:kill-switch"}}
]}`
}
