package executor

import (
	"context"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestPrivacyEnvelopeVerifyAcceptsCanonicalICMPv6Dependency(t *testing.T) {
	plan := productionShapedPrivacyEnvelopePlanForTest()
	runner := &recordingRunner{stdout: `table inet podlaz_pe_001122334455 {
	chain output {
		type filter hook output priority -10; policy accept;
		oifname "lo" counter packets 0 bytes 0 accept comment "podlaz:privacy-envelope:loopback"
		oifname "podlaz0" counter packets 1 bytes 64 accept comment "podlaz:privacy-envelope:tun-egress"
		ip daddr 192.0.2.10 counter packets 2 bytes 128 accept comment "podlaz:privacy-envelope:bootstrap"
		meta nfproto ipv4 udp sport 68 udp dport 67 counter packets 0 bytes 0 accept comment "podlaz:privacy-envelope:dhcp4"
		meta nfproto ipv6 udp sport 546 udp dport 547 counter packets 0 bytes 0 accept comment "podlaz:privacy-envelope:dhcp6"
		icmpv6 type { nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert } counter packets 0 bytes 0 accept comment "podlaz:privacy-envelope:ipv6-link-control"
		counter packets 0 bytes 0 reject with icmpx type port-unreachable comment "podlaz:privacy-envelope:block-direct"
	}
}`}

	if err := (PrivacyEnvelopeExecutor{Runner: runner}).Verify(context.Background(), plan); err != nil {
		t.Fatalf("canonical nftables rendering of the exact Privacy Envelope must verify: %v", err)
	}
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
