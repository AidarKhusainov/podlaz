package daemon

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestBootAutostartChildDeadlineDoesNotMasqueradeAsDaemonInterruption(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	if _, err := manifestStore.Enable(testBootAutostartConfig()); err != nil {
		t.Fatal(err)
	}

	lifecycle := &bootAutostartRecordingLifecycle{err: fmt.Errorf("verify connectivity: %w", context.DeadlineExceeded)}
	convergenceCalls := 0
	result, err := runBootAutostartStartup(
		context.Background(),
		manifestStore,
		attemptStore,
		continuation,
		lifecycle,
		func(context.Context) (bool, error) { return false, nil },
		func(ctx context.Context, continuation networkSessionContinuationStore) error {
			if ctx.Err() != nil {
				t.Fatalf("root context unexpectedly canceled: %v", ctx.Err())
			}
			convergenceCalls++
			state, exists, loadErr := continuation.stateStore().Load()
			if loadErr != nil {
				return loadErr
			}
			if !exists || state.Intent != networkSessionIntentTerminal {
				t.Fatalf("terminal convergence authority = %+v exists=%v", state, exists)
			}
			return nil
		},
	)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want wrapped context.DeadlineExceeded", err)
	}
	if result != bootAutostartStartupTerminal {
		t.Fatalf("result = %q, want %q", result, bootAutostartStartupTerminal)
	}
	if convergenceCalls != 1 {
		t.Fatalf("terminal convergence calls = %d, want 1", convergenceCalls)
	}
	attempt, exists, loadErr := attemptStore.LoadCurrent()
	if loadErr != nil || !exists {
		t.Fatalf("load attempt: exists=%v err=%v", exists, loadErr)
	}
	if attempt.State != bootAutostartAttemptTerminal {
		t.Fatalf("attempt state = %q, want terminal", attempt.State)
	}
}
