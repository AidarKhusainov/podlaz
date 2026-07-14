package executor

import (
	"context"
	"testing"
)

func TestResolvedDNSExecutorVerifyAcceptsMatchingDuplicateLinkRecord(t *testing.T) {
	runner := &recordingRunner{stdout: `Link 5 (podlaz0)
    Current Scopes: none

Link 7 (podlaz0)
    Current Scopes: none
         Protocols: +DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
Current DNS Server: 1.1.1.1
       DNS Servers: 1.1.1.1
        DNS Domain: ~.`}

	if err := (ResolvedDNSExecutor{Runner: runner, VerifyAttempts: 1}).Verify(context.Background(), dnsPlanForTest()); err != nil {
		t.Fatalf("a complete current podlaz0 record must win over a duplicate stale record: %v", err)
	}
}
