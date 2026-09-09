package executor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestParseNftTableJSONRejectsUnexpectedTableFlags(t *testing.T) {
	output := strings.Replace(canonicalPrivacyEnvelopeJSONForTest(),
		`"handle":10`, `"handle":10,"flags":["dormant"]`, 1)
	if _, err := parseNftTableJSON(output, "inet", "podlaz_pe_001122334455"); err == nil {
		t.Fatal("dormant owned table must fail closed")
	}
}

func TestParseNftTableJSONRejectsUnexpectedTableScopedObject(t *testing.T) {
	output := strings.Replace(canonicalPrivacyEnvelopeJSONForTest(),
		`{"chain":{"family":"inet"`,
		`{"set":{"family":"inet","table":"podlaz_pe_001122334455","name":"foreign","type":"ipv4_addr","handle":99}},`+
			`{"chain":{"family":"inet"`, 1)
	if _, err := parseNftTableJSON(output, "inet", "podlaz_pe_001122334455"); err == nil {
		t.Fatal("unexpected set inside owned table must fail closed")
	}
}

func TestParseNftTableJSONRejectsUnknownRuleStatement(t *testing.T) {
	output := strings.Replace(canonicalPrivacyEnvelopeJSONForTest(),
		`{"counter":{"packets":0,"bytes":0}},{"accept":null}`,
		`{"log":{"prefix":"foreign"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}`, 1)
	if _, err := parseNftTableJSON(output, "inet", "podlaz_pe_001122334455"); err == nil {
		t.Fatal("unknown rule statement must fail closed")
	}
}

func TestVerifyNftTableSnapshotRejectsChangedChainMetadata(t *testing.T) {
	snapshot, err := parseNftTableJSON(canonicalPrivacyEnvelopeJSONForTest(), "inet", "podlaz_pe_001122334455")
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	snapshot.Chains["output"] = nftChainSnapshot{
		Name: "output", Type: "filter", Hook: "input", Priority: -10, Policy: "accept",
		Rules: snapshot.Chains["output"].Rules,
	}
	plan := productionShapedPrivacyEnvelopePlanForTest()
	if err := verifyNftTableSnapshot(snapshot, planner.TunFirewallPlan{Chains: plan.Chains, Rules: plan.Rules}); err == nil {
		t.Fatal("changed hook must fail exact verification")
	}
}

func TestVerifyNftTableSnapshotRejectsRuleReorder(t *testing.T) {
	snapshot, err := parseNftTableJSON(canonicalPrivacyEnvelopeJSONForTest(), "inet", "podlaz_pe_001122334455")
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	chain := snapshot.Chains["output"]
	chain.Rules[0], chain.Rules[1] = chain.Rules[1], chain.Rules[0]
	snapshot.Chains["output"] = chain
	plan := productionShapedPrivacyEnvelopePlanForTest()
	if err := verifyNftTableSnapshot(snapshot, planner.TunFirewallPlan{Chains: plan.Chains, Rules: plan.Rules}); err == nil {
		t.Fatal("security-relevant rule reorder must fail exact verification")
	}
}

func TestVerifyNftTableSnapshotAcceptsCanonicalICMPv6ImplicitDependency(t *testing.T) {
	snapshot, err := parseNftTableJSON(canonicalPrivacyEnvelopeJSONForTest(), "inet", "podlaz_pe_001122334455")
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	plan := productionShapedPrivacyEnvelopePlanForTest()
	if err := verifyNftTableSnapshot(snapshot, planner.TunFirewallPlan{Chains: plan.Chains, Rules: plan.Rules}); err != nil {
		t.Fatalf("implicit ICMPv6 family dependency is semantic equivalence: %v", err)
	}
}

func TestObserveNftTablePresenceUsesStructuredEnumeration(t *testing.T) {
	runner := &recordingRunner{stdout: `{"nftables":[
{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},
{"table":{"family":"inet","name":"foreign","handle":1}},
{"table":{"family":"inet","name":"podlaz_pe_001122334455","handle":2}}
]}`}
	present, err := observeNftTablePresence(context.Background(), runner, "inet", "podlaz_pe_001122334455")
	if err != nil {
		t.Fatalf("observe presence: %v", err)
	}
	if !present {
		t.Fatal("exact enumerated table must be present")
	}
	if got, want := runner.commands, [][]string{{"nft", "-j", "list", "tables"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands=%#v, want %#v", got, want)
	}
}

func TestObserveNftTablePresenceProvesAbsenceOnlyFromSuccessfulEnumeration(t *testing.T) {
	runner := &recordingRunner{stdout: `{"nftables":[
{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},
{"table":{"family":"inet","name":"foreign","handle":1}}
]}`}
	present, err := observeNftTablePresence(context.Background(), runner, "inet", "podlaz_pe_001122334455")
	if err != nil {
		t.Fatalf("observe absence: %v", err)
	}
	if present {
		t.Fatal("non-enumerated exact identity must be absent")
	}
}

func TestObserveNftTablePresenceNeverConvertsInspectionFailureToAbsence(t *testing.T) {
	for _, runner := range []*recordingRunner{
		{stdout: `{"nftables":[{"metainfo":{"json_schema_version":1}},`},
		{err: errors.New("injected structured inspection failure")},
	} {
		present, err := observeNftTablePresence(context.Background(), runner, "inet", "podlaz_pe_001122334455")
		if err == nil {
			t.Fatalf("inspection failure must be unknown/error, got present=%v", present)
		}
	}
}

func TestFreshOwnedTableScriptsUseExclusiveCreate(t *testing.T) {
	privacy := privacyEnvelopePlanForTest("podlaz_pe_001122334455", "192.0.2.10")
	privacyScript, err := privacyEnvelopeApplyScript(privacy)
	if err != nil {
		t.Fatalf("privacy apply script: %v", err)
	}
	if !strings.HasPrefix(privacyScript, "create table inet "+privacy.Table+"\n") || strings.Contains(privacyScript, "add table inet "+privacy.Table) {
		t.Fatalf("privacy table creation must be exclusive:\n%s", privacyScript)
	}

	firewallPlan := testNftablesPlanForExclusiveCreate()
	firewallScript, err := nftablesApplyScript(firewallPlan)
	if err != nil {
		t.Fatalf("firewall apply script: %v", err)
	}
	if !strings.HasPrefix(firewallScript, "create table inet podlaz\n") || strings.Contains(firewallScript, "add table inet podlaz") {
		t.Fatalf("transaction-owned table creation must be exclusive:\n%s", firewallScript)
	}
}

func testNftablesPlanForExclusiveCreate() planner.TunFirewallPlan {
	return planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      "inet",
		Table:       "podlaz",
		TableAction: planner.FirewallTableAction,
		Chains: []planner.TunFirewallChainPlan{{
			Name: planner.FirewallOutputChain, Type: planner.FirewallChainTypeFilter,
			Hook: planner.FirewallOutputHook, Priority: 0, Policy: planner.FirewallDefaultChainPolicy,
			Action: planner.FirewallActionAdd,
		}},
		Rules: []planner.TunFirewallRulePlan{{
			Chain: planner.FirewallOutputChain, Expr: `oifname "lo"`, Verdict: planner.FirewallVerdictAccept,
			Action: planner.FirewallActionAdd, Ownership: planner.FirewallLoopbackOwner, RollbackKey: planner.FirewallLoopbackKey,
		}},
	}
}
