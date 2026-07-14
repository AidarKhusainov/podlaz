package executor

import (
	"context"
	"testing"
)

func TestResolvedDNSExecutorVerifyAcceptsConfiguredLinkWithoutActiveScope(t *testing.T) {
	plan := dnsPlanForTest()
	runner := &recordingRunner{stdout: `Link 7 (podlaz0)
    Current Scopes: none
         Protocols: +DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
Current DNS Server: 1.1.1.1
       DNS Servers: 1.1.1.1
        DNS Domain: ~.`}

	err := (ResolvedDNSExecutor{Runner: runner, VerifyAttempts: 1}).Verify(context.Background(), plan)
	if err != nil {
		t.Fatalf("configured per-link DNS must not depend on active Current Scopes: %v", err)
	}
}
