package executor

import (
	"context"
	"testing"
)

func TestResolvedDNSExecutorRollbackTreatsNoSuchDeviceAsAlreadyReverted(t *testing.T) {
	plan := dnsPlanForTest()
	runner := &recordingRunner{
		results: []CommandResult{{
			ExitCode: 1,
			Stderr:   `Failed to resolve interface "podlaz0": No such device`,
		}},
		errs: []error{executorTestExitError{code: 1}},
	}

	if err := (ResolvedDNSExecutor{Runner: runner}).Rollback(context.Background(), plan); err != nil {
		t.Fatalf("missing resolved link must be an idempotent rollback result: %v", err)
	}
}
