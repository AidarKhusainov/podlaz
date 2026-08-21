package daemon

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type bootAutostartCancelLifecycle struct{}

func (bootAutostartCancelLifecycle) Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{}, context.Canceled
}

func (bootAutostartCancelLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
}

func TestBootAutostartRestartCancellationKeepsPinnedAttemptForContinuation(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	if _, err := manifestStore.Enable(testBootAutostartConfig()); err != nil {
		t.Fatal(err)
	}
	lifecycle := newNetworkSessionLifecycle(bootAutostartCancelLifecycle{}, continuation)
	terminalConvergeCalls := 0

	result, err := runBootAutostartStartup(
		context.Background(),
		manifestStore,
		attemptStore,
		continuation,
		lifecycle,
		func(context.Context) (bool, error) { return false, nil },
		func(context.Context, networkSessionContinuationStore) error {
			terminalConvergeCalls++
			return nil
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("startup error = %v, want context.Canceled", err)
	}
	if result == bootAutostartStartupTerminal {
		t.Fatalf("restart cancellation consumed boot attempt as terminal")
	}
	if terminalConvergeCalls != 0 {
		t.Fatalf("restart cancellation ran terminal convergence %d time(s)", terminalConvergeCalls)
	}
	attempt, exists, loadErr := attemptStore.LoadCurrent()
	if loadErr != nil || !exists || attempt.State != bootAutostartAttemptInProgress {
		t.Fatalf("attempt after cancellation = %+v exists=%v err=%v", attempt, exists, loadErr)
	}
	request, exists, loadErr := continuation.LoadCurrent()
	if loadErr != nil || !exists {
		t.Fatalf("continuation after cancellation = exists=%v err=%v", exists, loadErr)
	}
	if request.Profile.ID != attempt.Configuration.Profile.ID || request.Mode != attempt.Configuration.Mode {
		t.Fatalf("continuation request does not match pinned attempt: request=%+v attempt=%+v", request, attempt.Configuration)
	}
}

func TestBootAutostartConnectFailureRequiresConclusiveTerminalConvergence(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	if _, err := manifestStore.Enable(testBootAutostartConfig()); err != nil {
		t.Fatal(err)
	}
	inner := &bootAutostartRecordingLifecycle{err: errors.New("simulated connect failure")}
	lifecycle := newNetworkSessionLifecycle(inner, continuation)
	convergenceErr := errors.New("simulated exact cleanup incomplete")

	result, err := runBootAutostartStartup(
		context.Background(),
		manifestStore,
		attemptStore,
		continuation,
		lifecycle,
		func(context.Context) (bool, error) { return false, nil },
		func(_ context.Context, terminal networkSessionContinuationStore) error {
			state, exists, loadErr := terminal.stateStore().Load()
			if loadErr != nil {
				return loadErr
			}
			if !exists || state.Intent != networkSessionIntentTerminal {
				t.Fatalf("terminal convergence started without durable terminal Network Session authority: %+v exists=%v", state, exists)
			}
			return convergenceErr
		},
	)
	if !errors.Is(err, convergenceErr) {
		t.Fatalf("startup error = %v, want convergence failure", err)
	}
	if result == bootAutostartStartupTerminal {
		t.Fatalf("incomplete cleanup was published as terminal")
	}
	attempt, exists, loadErr := attemptStore.LoadCurrent()
	if loadErr != nil || !exists || attempt.State != bootAutostartAttemptInProgress {
		t.Fatalf("attempt after incomplete cleanup = %+v exists=%v err=%v", attempt, exists, loadErr)
	}
	state, exists, loadErr := continuation.stateStore().Load()
	if loadErr != nil || !exists || state.Intent != networkSessionIntentTerminal {
		t.Fatalf("terminal cleanup authority was not retained: state=%+v exists=%v err=%v", state, exists, loadErr)
	}
}

func TestBootAutostartTerminalCompletionWriteFailureRetainsFailClosedAuthority(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	if _, err := manifestStore.Enable(testBootAutostartConfig()); err != nil {
		t.Fatal(err)
	}
	inner := &bootAutostartRecordingLifecycle{err: errors.New("simulated terminal connect failure")}
	lifecycle := newNetworkSessionLifecycle(inner, continuation)

	result, err := runBootAutostartStartup(
		context.Background(),
		manifestStore,
		attemptStore,
		continuation,
		lifecycle,
		func(context.Context) (bool, error) { return false, nil },
		func(_ context.Context, terminal networkSessionContinuationStore) error {
			state, exists, loadErr := terminal.stateStore().Load()
			if loadErr != nil || !exists || state.Intent != networkSessionIntentTerminal {
				t.Fatalf("terminal convergence authority = %+v exists=%v err=%v", state, exists, loadErr)
			}
			if err := os.Chmod(attemptStore.runtimeDir, 0o500); err != nil {
				t.Fatal(err)
			}
			return nil
		},
	)
	if err == nil {
		t.Fatal("startup unexpectedly succeeded after terminal attempt persistence was made unwritable")
	}
	if result == bootAutostartStartupTerminal {
		t.Fatalf("unpersisted terminal completion was treated as durable terminal")
	}
	if chmodErr := os.Chmod(attemptStore.runtimeDir, 0o700); chmodErr != nil {
		t.Fatal(chmodErr)
	}
	attempt, exists, loadErr := attemptStore.LoadCurrent()
	if loadErr != nil || !exists || attempt.State != bootAutostartAttemptInProgress {
		t.Fatalf("attempt after completion write failure = %+v exists=%v err=%v", attempt, exists, loadErr)
	}
	state, exists, loadErr := continuation.stateStore().Load()
	if loadErr != nil || !exists || state.Intent != networkSessionIntentTerminal {
		t.Fatalf("fail-closed terminal authority was lost: state=%+v exists=%v err=%v", state, exists, loadErr)
	}

	secondLifecycle := &bootAutostartRecordingLifecycle{}
	result, secondErr := runBootAutostartStartup(
		context.Background(),
		manifestStore,
		attemptStore,
		continuation,
		secondLifecycle,
		func(context.Context) (bool, error) { return false, nil },
		func(context.Context, networkSessionContinuationStore) error { return nil },
	)
	if secondErr != nil {
		t.Fatalf("second startup failed after storage recovered: %v", secondErr)
	}
	if result != bootAutostartStartupTerminal {
		t.Fatalf("second startup result = %q, want terminal", result)
	}
	if len(secondLifecycle.requests) != 0 {
		t.Fatalf("second startup issued %d fresh Connect call(s)", len(secondLifecycle.requests))
	}
}
