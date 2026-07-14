package executor

import (
	"context"
	"testing"
)

func TestResolvedDNSExecutorDefaultRetryWindowHandlesSlowResolvedPropagation(t *testing.T) {
	transient := CommandResult{Stdout: `Link 7 (podlaz0)
    Current Scopes: none
       DNS Servers: 1.1.1.1
        DNS Domain: ~.`}
	runner := &recordingRunner{results: []CommandResult{
		transient,
		transient,
		transient,
		transient,
		transient,
		{Stdout: resolvedStatusForTest},
	}}

	err := (ResolvedDNSExecutor{Runner: runner, Sleep: noResolvedDNSTestSleep}).Verify(context.Background(), dnsPlanForTest())
	if err != nil {
		t.Fatalf("default resolved propagation window must tolerate several delayed observations: %v", err)
	}
	if got := countResolvedStatusCommands(runner.commands); got != 6 {
		t.Fatalf("expected 6 status polls before convergence, got %d: %#v", got, runner.commands)
	}
}
