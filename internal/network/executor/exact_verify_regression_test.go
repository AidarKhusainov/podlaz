package executor

import (
	"context"
	"strings"
	"testing"
)

func TestNftablesExecutorVerifyRejectsExtraRuleInOwnedTable(t *testing.T) {
	plan := firewallPlanForTest()
	output := strings.Replace(
		nftablesListOutputForTest(),
		`\t\toifname != "podlaz0" counter reject comment "podlaz:firewall:kill-switch"`,
		`\t\tmeta l4proto tcp counter accept comment "foreign-extra"\n\t\toifname != "podlaz0" counter reject comment "podlaz:firewall:kill-switch"`,
		1,
	)
	if err := (NftablesExecutor{Runner: &recordingRunner{stdout: output}}).Verify(context.Background(), plan); err == nil {
		t.Fatal("expected exact nftables verification to reject an extra rule in the podlaz-owned table")
	}
}

func TestNftablesExecutorVerifyRejectsChainHookPriorityPolicyDrift(t *testing.T) {
	plan := firewallPlanForTest()
	output := strings.Replace(
		nftablesListOutputForTest(),
		"type filter hook output priority 0; policy accept;",
		"type filter hook output priority 10; policy drop;",
		1,
	)
	if err := (NftablesExecutor{Runner: &recordingRunner{stdout: output}}).Verify(context.Background(), plan); err == nil {
		t.Fatal("expected exact nftables verification to reject chain metadata drift")
	}
}

func TestResolvedDNSExecutorVerifyRejectsExtraDNSServer(t *testing.T) {
	plan := dnsPlanForTest()
	output := strings.Replace(resolvedStatusForTest, "DNS Servers: 1.1.1.1", "DNS Servers: 1.1.1.1 9.9.9.9", 1)
	if err := (ResolvedDNSExecutor{Runner: &recordingRunner{stdout: output}, VerifyAttempts: 1}).Verify(context.Background(), plan); err == nil {
		t.Fatal("expected exact resolved verification to reject an extra DNS server on podlaz0")
	}
}
