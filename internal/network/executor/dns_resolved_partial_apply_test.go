package executor

import (
	"context"
	"errors"
	"testing"
)

func TestResolvedDNSExecutorReturnsDNSOwnershipAfterPartialApply(t *testing.T) {
	runner := &recordingRunner{
		results: []CommandResult{{}, {}, {ExitCode: 2, Stderr: "domain update failed"}},
		errs:    []error{nil, nil, errors.New("domain update failed")},
	}
	executor := ResolvedDNSExecutor{Runner: runner, ApplyAttempts: 1}

	step, err := executor.Apply(context.Background(), dnsPlanForTest())
	if err == nil {
		t.Fatal("expected route-only domain apply failure")
	}
	if step.Kind != "dns" || step.Target != "podlaz0" || step.Owner != OwnerDNS {
		t.Fatalf("partial resolved mutation must return DNS rollback ownership: %#v", step)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("unexpected command count after partial DNS apply: %#v", runner.commands)
	}
}
