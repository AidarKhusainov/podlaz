package executor

import (
	"context"
	"strings"
	"testing"
)

func TestResolvedDNSExecutorVerifyRejectsDuplicateTargetLinkRecords(t *testing.T) {
	runner := &recordingRunner{stdout: `Link 5 (podlaz0)
    Current Scopes: none
         Protocols: +DefaultRoute
       DNS Servers: 1.1.1.1
        DNS Domain: ~.

Link 7 (podlaz0)
    Current Scopes: none
         Protocols: +DefaultRoute
       DNS Servers: 9.9.9.9
        DNS Domain: corp.example.test`}

	err := (ResolvedDNSExecutor{Runner: runner, VerifyAttempts: 1}).Verify(context.Background(), dnsPlanForTest())
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("ambiguous duplicate podlaz0 sections must fail closed, got %v", err)
	}
}
