package executor

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestPrivacyEnvelopeStructuredVerifierRejectsExactSemanticDriftMatrix(t *testing.T) {
	base, err := parseNftTableJSON(canonicalPrivacyEnvelopeJSONForTest(), "inet", "podlaz_pe_001122334455")
	if err != nil {
		t.Fatalf("parse canonical fixture: %v", err)
	}
	plan := productionShapedPrivacyEnvelopePlanForTest()
	expected := planner.TunFirewallPlan{Chains: plan.Chains, Rules: plan.Rules}

	tests := map[string]func(nftTableSnapshot) nftTableSnapshot{
		"missing rule": func(snapshot nftTableSnapshot) nftTableSnapshot {
			chain := snapshot.Chains["output"]
			chain.Rules = chain.Rules[:len(chain.Rules)-1]
			snapshot.Chains["output"] = chain
			return snapshot
		},
		"changed bootstrap endpoint": func(snapshot nftTableSnapshot) nftTableSnapshot {
			return mutateObservedRuleStatement(snapshot, 2, 0, "match=payload:ip:daddr:==192.0.2.11")
		},
		"changed TUN predicate": func(snapshot nftTableSnapshot) nftTableSnapshot {
			return mutateObservedRuleStatement(snapshot, 1, 0, "match=meta:oifname:==podlaz1")
		},
		"changed verdict": func(snapshot nftTableSnapshot) nftTableSnapshot {
			return mutateObservedRuleStatement(snapshot, 0, 2, "drop")
		},
		"additional predicate": func(snapshot nftTableSnapshot) nftTableSnapshot {
			chain := snapshot.Chains["output"]
			rule := append(nftRuleSnapshot(nil), chain.Rules[0]...)
			rule = append(rule[:1], append(nftRuleSnapshot{"match=meta:nfproto:==ipv4"}, rule[1:]...)...)
			chain.Rules = append([]nftRuleSnapshot(nil), chain.Rules...)
			chain.Rules[0] = rule
			snapshot.Chains["output"] = chain
			return snapshot
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			snapshot := mutate(cloneNftTableSnapshotForTest(base))
			if err := verifyNftTableSnapshot(snapshot, expected); err == nil {
				t.Fatalf("%s must fail exact semantic verification", name)
			}
		})
	}
}

func TestParseNftTableJSONRejectsDuplicateTableAndChainIdentities(t *testing.T) {
	base := canonicalPrivacyEnvelopeJSONForTest()
	tableObject := `{"table":{"family":"inet","name":"podlaz_pe_001122334455","handle":10}}`
	chainObject := `{"chain":{"family":"inet","table":"podlaz_pe_001122334455","name":"output","handle":1,"type":"filter","hook":"output","prio":-10,"policy":"accept"}}`

	for name, output := range map[string]string{
		"duplicate table": strings.Replace(base, tableObject, tableObject+","+tableObject, 1),
		"duplicate chain": strings.Replace(base, chainObject, chainObject+","+chainObject, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if output == base {
				t.Fatal("test fixture did not inject duplicate identity")
			}
			if _, err := parseNftTableJSON(output, "inet", "podlaz_pe_001122334455"); err == nil {
				t.Fatalf("%s must fail closed", name)
			}
		})
	}
}

func TestPrivacyEnvelopeStructuredVerifierTreatsICMPv6SetMemberOrderAsSemantic(t *testing.T) {
	output := strings.Replace(
		canonicalPrivacyEnvelopeJSONForTest(),
		`"set":["nd-router-solicit","nd-neighbor-solicit","nd-neighbor-advert"]`,
		`"set":["nd-neighbor-advert","nd-router-solicit","nd-neighbor-solicit"]`,
		1,
	)
	if output == canonicalPrivacyEnvelopeJSONForTest() {
		t.Fatal("test fixture did not reorder ICMPv6 set members")
	}
	plan := productionShapedPrivacyEnvelopePlanForTest()
	if err := VerifyNftablesTableOutput(
		planner.TunFirewallPlan{Family: plan.Family, Table: plan.Table, Chains: plan.Chains, Rules: plan.Rules},
		output,
	); err != nil {
		t.Fatalf("ICMPv6 set member order must not create semantic drift: %v", err)
	}
}

func mutateObservedRuleStatement(snapshot nftTableSnapshot, ruleIndex, statementIndex int, value string) nftTableSnapshot {
	chain := snapshot.Chains["output"]
	chain.Rules = append([]nftRuleSnapshot(nil), chain.Rules...)
	rule := append(nftRuleSnapshot(nil), chain.Rules[ruleIndex]...)
	rule[statementIndex] = value
	chain.Rules[ruleIndex] = rule
	snapshot.Chains["output"] = chain
	return snapshot
}

func cloneNftTableSnapshotForTest(snapshot nftTableSnapshot) nftTableSnapshot {
	clone := snapshot
	clone.Flags = append([]string(nil), snapshot.Flags...)
	clone.Chains = make(map[string]nftChainSnapshot, len(snapshot.Chains))
	for name, chain := range snapshot.Chains {
		chainCopy := chain
		chainCopy.Rules = make([]nftRuleSnapshot, len(chain.Rules))
		for i, rule := range chain.Rules {
			chainCopy.Rules[i] = append(nftRuleSnapshot(nil), rule...)
		}
		clone.Chains[name] = chainCopy
	}
	return clone
}
