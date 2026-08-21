package daemon

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type bootAutostartCancelLifecycle struct {
	cancel context.CancelFunc
}

func (l bootAutostartCancelLifecycle) Connect(context.Context, api.ConnectRequest) (api.LifecycleResponse, error) {
	if l.cancel != nil {
		l.cancel()
	}
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
	ctx, cancel := context.WithCancel(context.Background())
	lifecycle := newNetworkSessionLifecycle(bootAutostartCancelLifecycle{cancel: cancel}, continuation)
	terminalConvergeCalls := 0

	result, err := runBootAutostartStartup(
		ctx,
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
	if !errors.Is(err, convergenceErr) || result != bootAutostartStartupRecoveryFailed {
		t.Fatalf("incomplete convergence result=%q err=%v", result, err)
	}
	attempt, exists, loadErr := attemptStore.LoadCurrent()
	if loadErr != nil || !exists || attempt.State != bootAutostartAttemptInProgress {
		t.Fatalf("incomplete cleanup consumed boot attempt: %+v exists=%v err=%v", attempt, exists, loadErr)
	}
	state, exists, loadErr := continuation.stateStore().Load()
	if loadErr != nil || !exists || state.Intent != networkSessionIntentTerminal {
		t.Fatalf("incomplete cleanup lost terminal continuation: %+v exists=%v err=%v", state, exists, loadErr)
	}
}

func TestBootAutostartTerminalCompletionWriteFailureStaysFailClosed(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	if _, err := manifestStore.Enable(testBootAutostartConfig()); err != nil {
		t.Fatal(err)
	}
	lifecycle := &bootAutostartRecordingLifecycle{err: errors.New("simulated connect failure")}
	result, err := runBootAutostartStartup(
		context.Background(),
		manifestStore,
		attemptStore,
		continuation,
		lifecycle,
		func(context.Context) (bool, error) { return false, nil },
		func(_ context.Context, terminal networkSessionContinuationStore) error {
			if err := os.RemoveAll(attemptStore.runtimeDir); err != nil {
				return err
			}
			if err := os.WriteFile(attemptStore.runtimeDir, []byte("block directory recreation"), 0o600); err != nil {
				return err
			}
			return nil
		},
	)
	if err == nil || result != bootAutostartStartupRecoveryFailed {
		t.Fatalf("completion write failure result=%q err=%v", result, err)
	}
	state, exists, loadErr := continuation.stateStore().Load()
	if loadErr != nil || !exists || state.Intent != networkSessionIntentTerminal {
		t.Fatalf("completion write failure lost fail-closed terminal authority: %+v exists=%v err=%v", state, exists, loadErr)
	}
}

func TestBootAutostartSuccessfulCompletionWriteFailureRetainsResumeAuthority(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	if _, err := manifestStore.Enable(testBootAutostartConfig()); err != nil {
		t.Fatal(err)
	}
	lifecycle := &bootAutostartRecordingLifecycle{}
	continuation.afterSave = func() {
		if err := os.RemoveAll(attemptStore.runtimeDir); err != nil {
			t.Fatalf("remove attempt dir: %v", err)
		}
		if err := os.WriteFile(attemptStore.runtimeDir, []byte("block directory recreation"), 0o600); err != nil {
			t.Fatalf("replace attempt dir: %v", err)
		}
	}

	result, err := runBootAutostartStartup(
		context.Background(),
		manifestStore,
		attemptStore,
		continuation,
		lifecycle,
		func(context.Context) (bool, error) { return false, nil },
		successfulBootTerminalConvergence(t),
	)
	if err == nil || result != bootAutostartStartupRecoveryFailed {
		t.Fatalf("successful completion write failure result=%q err=%v", result, err)
	}
	request, exists, loadErr := continuation.LoadCurrent()
	if loadErr != nil || !exists {
		t.Fatalf("successful completion write failure lost resume authority: exists=%v err=%v", exists, loadErr)
	}
	if request.Profile.ID == "" {
		t.Fatalf("successful completion write failure retained empty request")
	}
}
