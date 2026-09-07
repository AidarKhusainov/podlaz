package executor

import (
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
