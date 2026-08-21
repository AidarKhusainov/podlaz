package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type bootAutostartRecordingLifecycle struct {
	requests []api.ConnectRequest
	err      error
}

func (l *bootAutostartRecordingLifecycle) Connect(_ context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	l.requests = append(l.requests, request)
	if l.err != nil {
		return api.LifecycleResponse{}, l.err
	}
	return api.LifecycleResponse{
		Connection:  "active",
		Mode:        request.Mode,
		ProfileID:   request.Profile.ID,
		ProfileName: request.Profile.Name,
		Proxy:       "inactive",
		TUN:         "active",
	}, nil
}

func (l *bootAutostartRecordingLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
}

func bootAutostartStores(t *testing.T, configuredBoot, currentBoot string) (bootAutostartManifestStore, bootAutostartAttemptStore, networkSessionContinuationStore) {
	t.Helper()
	manifestStore := newBootAutostartManifestStore(t.TempDir(), fixedBootID(configuredBoot))
	attemptStore := newBootAutostartAttemptStore(t.TempDir(), fixedBootID(currentBoot))
	continuation := newNetworkSessionContinuationStore(t.TempDir(), fixedBootID(currentBoot))
	return manifestStore, attemptStore, continuation
}

func successfulBootTerminalConvergence(t *testing.T) bootAutostartTerminalConvergeFunc {
	t.Helper()
	return func(_ context.Context, continuation networkSessionContinuationStore) error {
		state, exists, err := continuation.stateStore().Load()
		if err != nil {
			return err
		}
		if !exists || state.Intent != networkSessionIntentTerminal {
			t.Fatalf("terminal convergence authority = %+v exists=%v", state, exists)
		}
		return nil
	}
}

func bootRequest(configuration api.AutostartConfigureRequest) api.ConnectRequest {
	return api.ConnectRequest{Mode: configuration.Mode, Profile: configuration.Profile}
}

func TestRunBootAutostartStartupDoesNothingWhenDisabled(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	lifecycle := &bootAutostartRecordingLifecycle{}
	resumeCalls := 0

	result, err := runBootAutostartStartup(context.Background(), manifestStore, attemptStore, continuation, lifecycle, func(context.Context) (bool, error) {
		resumeCalls++
		return false, nil
	})
	if err != nil {
		t.Fatalf("runBootAutostartStartup() error = %v", err)
	}
	if result != bootAutostartStartupNoop || resumeCalls != 1 || len(lifecycle.requests) != 0 {
		t.Fatalf("result=%q resumeCalls=%d connectCalls=%d", result, resumeCalls, len(lifecycle.requests))
	}
	if _, exists, err := attemptStore.LoadCurrent(); err != nil || exists {
		t.Fatalf("disabled autostart created attempt: exists=%v err=%v", exists, err)
	}
}

func TestRunBootAutostartStartupDoesNotUseManifestConfiguredThisBoot(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootAttempt, testBootAttempt)
	if _, err := manifestStore.Enable(testBootAutostartConfig()); err != nil {
		t.Fatal(err)
	}
	lifecycle := &bootAutostartRecordingLifecycle{}
	result, err := runBootAutostartStartup(context.Background(), manifestStore, attemptStore, continuation, lifecycle, func(context.Context) (bool, error) { return false, nil })
	if err != nil || result != bootAutostartStartupNoop || len(lifecycle.requests) != 0 {
		t.Fatalf("same-boot configuration triggered connect: result=%q requests=%d err=%v", result, len(lifecycle.requests), err)
	}
	if _, exists, err := attemptStore.LoadCurrent(); err != nil || exists {
		t.Fatalf("same-boot configuration created attempt: exists=%v err=%v", exists, err)
	}
}

func TestRunBootAutostartStartupAdmitsOneFutureBootConnectWithCanonicalDefaults(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	config := testBootAutostartConfig()
	if _, err := manifestStore.Enable(config); err != nil {
		t.Fatal(err)
	}
	lifecycle := &bootAutostartRecordingLifecycle{}

	result, err := runBootAutostartStartup(context.Background(), manifestStore, attemptStore, continuation, lifecycle, func(context.Context) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	if result != bootAutostartStartupConnected || len(lifecycle.requests) != 1 {
		t.Fatalf("result=%q requests=%d", result, len(lifecycle.requests))
	}
	want := bootRequest(config)
	if !reflect.DeepEqual(lifecycle.requests[0], want) {
		t.Fatalf("boot connect request = %#v, want %#v", lifecycle.requests[0], want)
	}
	if lifecycle.requests[0].Handoff != "" {
		t.Fatalf("boot connect persisted legacy handoff semantics: %q", lifecycle.requests[0].Handoff)
	}
	attempt, exists, err := attemptStore.LoadCurrent()
	if err != nil || !exists || attempt.State != bootAutostartAttemptSucceeded {
		t.Fatalf("attempt after connect = %+v exists=%v err=%v", attempt, exists, err)
	}
	if _, exists, err := continuation.LoadCurrent(); err != nil || !exists {
		t.Fatalf("successful boot connect did not retain same-boot continuation: exists=%v err=%v", exists, err)
	}
}

func TestRunBootAutostartStartupReplaysPinnedInProgressAttemptAfterPreContinuationCrash(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	manifest, err := manifestStore.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := attemptStore.Admit(manifest)
	if err != nil {
		t.Fatal(err)
	}

	changedStore := newBootAutostartManifestStore(manifestStore.stateDir, fixedBootID(testBootAttempt))
	changed := testBootAutostartConfig()
	changed.Profile.ID = "future-profile"
	changed.Profile.Name = "Future Example VPN"
	if _, err := changedStore.Enable(changed); err != nil {
		t.Fatal(err)
	}

	lifecycle := &bootAutostartRecordingLifecycle{}
	result, err := runBootAutostartStartup(context.Background(), changedStore, attemptStore, continuation, lifecycle, func(context.Context) (bool, error) { return false, nil })
	if err != nil || result != bootAutostartStartupConnected || len(lifecycle.requests) != 1 {
		t.Fatalf("result=%q requests=%d err=%v", result, len(lifecycle.requests), err)
	}
	want := bootRequest(attempt.Configuration)
	if !reflect.DeepEqual(lifecycle.requests[0], want) {
		t.Fatalf("replay used changed manifest: got %#v want pinned %#v", lifecycle.requests[0], want)
	}
}

func TestRunBootAutostartStartupContinuationAlwaysWins(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	manifest, err := manifestStore.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attemptStore.Admit(manifest); err != nil {
		t.Fatal(err)
	}
	if err := continuation.Save(bootRequest(manifest.Configuration)); err != nil {
		t.Fatal(err)
	}
	lifecycle := &bootAutostartRecordingLifecycle{}
	resumeCalls := 0

	result, err := runBootAutostartStartup(context.Background(), manifestStore, attemptStore, continuation, lifecycle, func(context.Context) (bool, error) {
		resumeCalls++
		return true, nil
	})
	if err != nil || result != bootAutostartStartupContinued || resumeCalls != 1 || len(lifecycle.requests) != 0 {
		t.Fatalf("continuation ordering: result=%q resume=%d requests=%d err=%v", result, resumeCalls, len(lifecycle.requests), err)
	}
	attempt, exists, err := attemptStore.LoadCurrent()
	if err != nil || !exists || attempt.State != bootAutostartAttemptSucceeded {
		t.Fatalf("resumed attempt = %+v exists=%v err=%v", attempt, exists, err)
	}
}

func TestRunBootAutostartStartupConvergedTerminalContinuationDoesNotFreshConnect(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	manifest, err := manifestStore.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attemptStore.Admit(manifest); err != nil {
		t.Fatal(err)
	}
	if err := continuation.Save(bootRequest(manifest.Configuration)); err != nil {
		t.Fatal(err)
	}
	if err := continuation.disarm(networkSessionIntentTerminal); err != nil {
		t.Fatal(err)
	}
	lifecycle := &bootAutostartRecordingLifecycle{}
	resumeCalls := 0

	result, err := runBootAutostartStartup(
		context.Background(), manifestStore, attemptStore, continuation, lifecycle,
		func(context.Context) (bool, error) {
			resumeCalls++
			return false, nil
		},
		successfulBootTerminalConvergence(t),
	)
	if err != nil || result != bootAutostartStartupTerminal || len(lifecycle.requests) != 0 || resumeCalls != 0 {
		t.Fatalf("terminal continuation result=%q requests=%d resume=%d err=%v", result, len(lifecycle.requests), resumeCalls, err)
	}
	attempt, exists, err := attemptStore.LoadCurrent()
	if err != nil || !exists || attempt.State != bootAutostartAttemptTerminal {
		t.Fatalf("terminal attempt = %+v exists=%v err=%v", attempt, exists, err)
	}
	if _, exists, err := continuation.stateStore().Load(); err != nil || exists {
		t.Fatalf("converged terminal continuation remained: exists=%v err=%v", exists, err)
	}
}

func TestRunBootAutostartStartupCompletedAttemptNeverReconnectsSameBoot(t *testing.T) {
	for _, complete := range []struct {
		name string
		fn   func(bootAutostartAttemptStore) error
	}{
		{"succeeded", func(store bootAutostartAttemptStore) error { return store.MarkSucceeded() }},
		{"terminal", func(store bootAutostartAttemptStore) error { return store.MarkTerminal(bootAutostartTerminalConnectFailed) }},
	} {
		t.Run(complete.name, func(t *testing.T) {
			manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
			manifest, err := manifestStore.Enable(testBootAutostartConfig())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := attemptStore.Admit(manifest); err != nil {
				t.Fatal(err)
			}
			if err := complete.fn(attemptStore); err != nil {
				t.Fatal(err)
			}
			lifecycle := &bootAutostartRecordingLifecycle{}
			result, err := runBootAutostartStartup(context.Background(), manifestStore, attemptStore, continuation, lifecycle, func(context.Context) (bool, error) { return false, nil })
			if err != nil || result != bootAutostartStartupNoop || len(lifecycle.requests) != 0 {
				t.Fatalf("completed attempt reconnected: result=%q requests=%d err=%v", result, len(lifecycle.requests), err)
			}
		})
	}
}

func TestRunBootAutostartStartupConnectFailureConsumesAttemptOnlyAfterConvergence(t *testing.T) {
	manifestStore, attemptStore, continuation := bootAutostartStores(t, testBootConfigured, testBootAttempt)
	if _, err := manifestStore.Enable(testBootAutostartConfig()); err != nil {
		t.Fatal(err)
	}
	lifecycle := &bootAutostartRecordingLifecycle{err: errors.New("simulated terminal connect failure")}
	result, err := runBootAutostartStartup(
		context.Background(), manifestStore, attemptStore, continuation, lifecycle,
		func(context.Context) (bool, error) { return false, nil },
		successfulBootTerminalConvergence(t),
	)
	if err == nil || result != bootAutostartStartupTerminal || len(lifecycle.requests) != 1 {
		t.Fatalf("connect failure result=%q requests=%d err=%v", result, len(lifecycle.requests), err)
	}
	attempt, exists, loadErr := attemptStore.LoadCurrent()
	if loadErr != nil || !exists || attempt.State != bootAutostartAttemptTerminal {
		t.Fatalf("failed connect attempt = %+v exists=%v err=%v", attempt, exists, loadErr)
	}
	if _, exists, loadErr := continuation.stateStore().Load(); loadErr != nil || exists {
		t.Fatalf("terminal completion left continuation: exists=%v err=%v", exists, loadErr)
	}

	secondLifecycle := &bootAutostartRecordingLifecycle{}
	result, secondErr := runBootAutostartStartup(context.Background(), manifestStore, attemptStore, continuation, secondLifecycle, func(context.Context) (bool, error) { return false, nil })
	if secondErr != nil || result != bootAutostartStartupNoop || len(secondLifecycle.requests) != 0 {
		t.Fatalf("terminal attempt retried: result=%q requests=%d err=%v", result, len(secondLifecycle.requests), secondErr)
	}
}
