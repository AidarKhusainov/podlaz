package executor

import (
	"context"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestPrivacyEnvelopeVerifyAcceptsCanonicalICMPv6Dependency(t *testing.T) {
	plan := productionShapedPrivacyEnvelopePlanForTest()
	runner := canonicalPrivacyEnvelopeRunner{}

	if err := (PrivacyEnvelopeExecutor{Runner: runner}).Verify(context.Background(), plan); err != nil {
		t.Fatalf("canonical nftables rendering of the exact Privacy Envelope must verify: %v", err)
	}
}

type canonicalPrivacyEnvelopeRunner struct{}

func (canonicalPrivacyEnvelopeRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	if name != "nft" {
		return CommandResult{}, nil
	}
	for _, arg := range args {
		if arg == "-j" || arg == "--json" {
			return CommandResult{Stdout: canonicalPrivacyEnvelopeJSONForTest()}, nil
		}
	}
	return CommandResult{Stdout: canonicalPrivacyEnvelopeTextForTest()}, nil
}

func productionShapedPrivacyEnvelopePlanForTest() PrivacyEnvelopePlan {
	const (
		chain = "output"
		add   = planner.FirewallActionAdd
	)
	return PrivacyEnvelopePlan{
		Family: "inet",
		Table:  "podlaz_pe_001122334455",
		Chains: []planner.TunFirewallChainPlan{{
			Name:     chain,
			Type:     planner.FirewallChainTypeFilter,
			Hook:     planner.FirewallOutputHook,
			Priority: -10,
			Policy:   planner.FirewallDefaultChainPolicy,
			Action:   add,
		}},
		Rules: []planner.TunFirewallRulePlan{
			{Chain: chain, Expr: `oifname "lo"`, Verdict: planner.FirewallVerdictAccept, Action: add, Ownership: "podlaz:privacy-envelope:loopback"},
			{Chain: chain, Expr: `oifname "podlaz0"`, Verdict: planner.FirewallVerdictAccept, Action: add, Ownership: "podlaz:privacy-envelope:tun-egress"},
			{Chain: chain, Expr: "ip daddr 192.0.2.10", Verdict: planner.FirewallVerdictAccept, Action: add, Ownership: "podlaz:privacy-envelope:bootstrap"},
			{Chain: chain, Expr: "meta nfproto ipv4 udp sport 68 udp dport 67", Verdict: planner.FirewallVerdictAccept, Action: add, Ownership: "podlaz:privacy-envelope:dhcp4"},
			{Chain: chain, Expr: "meta nfproto ipv6 udp sport 546 udp dport 547", Verdict: planner.FirewallVerdictAccept, Action: add, Ownership: "podlaz:privacy-envelope:dhcp6"},
			{Chain: chain, Expr: "meta nfproto ipv6 icmpv6 type { nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert }", Verdict: planner.FirewallVerdictAccept, Action: add, Ownership: "podlaz:privacy-envelope:ipv6-link-control"},
			{Chain: chain, Verdict: planner.FirewallVerdictReject, Action: add, Ownership: "podlaz:privacy-envelope:block-direct"},
		},
	}
}

func canonicalPrivacyEnvelopeTextForTest() string {
	return `table inet podlaz_pe_001122334455 {
	chain output {
		type filter hook output priority -10; policy accept;
		oifname "lo" counter accept comment "podlaz:privacy-envelope:loopback"
		oifname "podlaz0" counter accept comment "podlaz:privacy-envelope:tun-egress"
		ip daddr 192.0.2.10 counter accept comment "podlaz:privacy-envelope:bootstrap"
		meta nfproto ipv4 udp sport 68 udp dport 67 counter accept comment "podlaz:privacy-envelope:dhcp4"
		meta nfproto ipv6 udp sport 546 udp dport 547 counter accept comment "podlaz:privacy-envelope:dhcp6"
		icmpv6 type { nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert } counter accept comment "podlaz:privacy-envelope:ipv6-link-control"
		counter reject comment "podlaz:privacy-envelope:block-direct"
	}
}`
}

func canonicalPrivacyEnvelopeJSONForTest() string {
	return `{"nftables":[
{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},
{"table":{"family":"inet","name":"podlaz_pe_001122334455","handle":10}},
{"chain":{"family":"inet","table":"podlaz_pe_001122334455","name":"output","handle":1,"type":"filter","hook":"output","prio":-10,"policy":"accept"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":1,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"lo"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:privacy-envelope:loopback"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":2,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"podlaz0"}},{"counter":{"packets":1,"bytes":64}},{"accept":null}],"comment":"podlaz:privacy-envelope:tun-egress"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":3,"expr":[{"match":{"op":"==","left":{"meta":{"key":"nfproto"}},"right":"ipv4"}},{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"192.0.2.10"}},{"counter":{"packets":2,"bytes":128}},{"accept":null}],"comment":"podlaz:privacy-envelope:bootstrap"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":4,"expr":[{"match":{"op":"==","left":{"meta":{"key":"nfproto"}},"right":"ipv4"}},{"match":{"op":"==","left":{"meta":{"key":"l4proto"}},"right":"udp"}},{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"sport"}},"right":68}},{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"dport"}},"right":67}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:privacy-envelope:dhcp4"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":5,"expr":[{"match":{"op":"==","left":{"meta":{"key":"nfproto"}},"right":"ipv6"}},{"match":{"op":"==","left":{"meta":{"key":"l4proto"}},"right":"udp"}},{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"sport"}},"right":546}},{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"dport"}},"right":547}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:privacy-envelope:dhcp6"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":6,"expr":[{"match":{"op":"==","left":{"meta":{"key":"l4proto"}},"right":"ipv6-icmp"}},{"match":{"op":"==","left":{"payload":{"protocol":"icmpv6","field":"type"}},"right":{"set":["nd-router-solicit","nd-neighbor-solicit","nd-neighbor-advert"]}}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:privacy-envelope:ipv6-link-control"}},
{"rule":{"family":"inet","table":"podlaz_pe_001122334455","chain":"output","handle":7,"expr":[{"counter":{"packets":0,"bytes":0}},{"reject":{"type":"icmpx","expr":"port-unreachable"}}],"comment":"podlaz:privacy-envelope:block-direct"}}
]}`
}

func TestProductionShapedPrivacyEnvelopeFixtureHasSevenOrderedRules(t *testing.T) {
	plan := productionShapedPrivacyEnvelopePlanForTest()
	if got, want := len(plan.Rules), 7; got != want {
		t.Fatalf("rules=%d, want %d", got, want)
	}
	if got, want := plan.Rules[5].Ownership, "podlaz:privacy-envelope:ipv6-link-control"; got != want {
		t.Fatalf("rule[5] owner=%q, want %q", got, want)
	}
	if !reflect.DeepEqual(plan.Rules[6].Verdict, planner.FirewallVerdictReject) {
		t.Fatalf("rule[6] must remain terminal reject: %#v", plan.Rules[6])
	}
}
