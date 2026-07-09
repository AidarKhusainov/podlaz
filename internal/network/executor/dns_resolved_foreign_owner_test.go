package executor

import (
	"context"
	"strings"
	"testing"
)

func TestResolvedDNSExecutorVerifyRejectsForeignRouteOnlyDNSOwnerBeforeTargetLinkDelay(t *testing.T) {
	plan := dnsPlanForTest()
	runner := &recordingRunner{stdout: `Link 2 (wlan0)
    Current Scopes: DNS
Current DNS Server: 198.51.100.53
       DNS Servers: 198.51.100.53
        DNS Domain: ~.`}

	err := (ResolvedDNSExecutor{Runner: runner, VerifyAttempts: 3, Sleep: noResolvedDNSTestSleep}).Verify(context.Background(), plan)
	if err == nil {
		t.Fatal("expected foreign route-only DNS owner failure")
	}
	if !strings.Contains(err.Error(), "foreign route-only DNS owner wlan0 still has ~.") {
		t.Fatalf("expected foreign route-only DNS owner failure, got %v", err)
	}
	if strings.Contains(err.Error(), "link status not found") {
		t.Fatalf("foreign route-only DNS owner must not be masked as target-link propagation delay: %v", err)
	}
	if got := countResolvedStatusCommands(runner.commands); got != 1 {
		t.Fatalf("foreign route-only DNS owner must fail without polling retries, got %d status calls: %#v", got, runner.commands)
	}
}
