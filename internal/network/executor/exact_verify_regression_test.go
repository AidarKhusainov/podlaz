package executor

import (
	"context"
	"strings"
	"testing"
)

func TestNftablesExecutorVerifyRejectsExtraRuleInOwnedTable(t *testing.T) {
	plan := firewallPlanForTest()
	needle := `{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":4`
	extra := `{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":99,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"lo"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"foreign-extra"}},\n`
	output := strings.Replace(nftablesJSONForTest(), needle, extra+needle, 1)
	if output == nftablesJSONForTest() {
		t.Fatal("test fixture did not inject the extra nftables rule")
	}
	if err := (NftablesExecutor{Runner: &privacyEnvelopeRecordingRunner{tableJSON: output}}).Verify(context.Background(), plan); err == nil {
		t.Fatal("expected exact nftables verification to reject an extra rule in the podlaz-owned table")
	}
}

func TestNftablesExecutorVerifyRejectsChainHookPriorityPolicyDrift(t *testing.T) {
	plan := firewallPlanForTest()
	output := strings.Replace(
		nftablesJSONForTest(),
		`"hook":"output","prio":0,"policy":"accept"`,
		`"hook":"output","prio":10,"policy":"drop"`,
		1,
	)
	if err := (NftablesExecutor{Runner: &privacyEnvelopeRecordingRunner{tableJSON: output}}).Verify(context.Background(), plan); err == nil {
		t.Fatal("expected exact nftables verification to reject chain metadata drift")
	}
}

func TestNftablesExecutorVerifyAcceptsCanonicalDefaultRejectRendering(t *testing.T) {
	plan := firewallPlanForTest()
	if err := (NftablesExecutor{Runner: &privacyEnvelopeRecordingRunner{tableJSON: nftablesJSONForTest()}}).Verify(context.Background(), plan); err != nil {
		t.Fatalf("canonical structured default inet reject must remain semantically exact: %v", err)
	}
}

func TestResolvedDNSExecutorVerifyRejectsExtraDNSServer(t *testing.T) {
	plan := dnsPlanForTest()
	output := strings.Replace(resolvedStatusForTest, "DNS Servers: 1.1.1.1", "DNS Servers: 1.1.1.1 9.9.9.9", 1)
	if err := (ResolvedDNSExecutor{Runner: &recordingRunner{stdout: output}, VerifyAttempts: 1}).Verify(context.Background(), plan); err == nil {
		t.Fatal("expected exact resolved verification to reject an extra DNS server on podlaz0")
	}
}
