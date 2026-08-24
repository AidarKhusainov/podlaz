package daemon

import (
	"context"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

type productReasonSuccessfulLifecycle struct{}

func (productReasonSuccessfulLifecycle) Connect(_ context.Context, request api.ConnectRequest) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{
		Connection:  "active",
		Mode:        request.Mode,
		ProfileID:   request.Profile.ID,
		ProfileName: request.Profile.Name,
		Proxy:       "inactive",
		TUN:         "active",
	}, nil
}

func (productReasonSuccessfulLifecycle) Disconnect(context.Context) (api.LifecycleResponse, error) {
	return api.LifecycleResponse{Connection: "inactive", Proxy: "inactive", TUN: "disabled"}, nil
}

func terminalBootAttemptForProductReasonTest(t *testing.T, runtimeDir string) (bootAutostartAttemptStore, productTerminalReasonStore) {
	t.Helper()
	manifestStore := newBootAutostartManifestStore(t.TempDir(), fixedBootID(testBootConfigured))
	manifest, err := manifestStore.Enable(testBootAutostartConfig())
	if err != nil {
		t.Fatal(err)
	}
	attemptStore := newBootAutostartAttemptStore(runtimeDir, fixedBootID(testBootAttempt))
	if _, err := attemptStore.Admit(manifest); err != nil {
		t.Fatal(err)
	}
	if err := attemptStore.MarkTerminal(bootAutostartTerminalConnectFailed); err != nil {
		t.Fatal(err)
	}
	return attemptStore, newProductTerminalReasonStore(runtimeDir, fixedBootID(testBootAttempt))
}

func TestManualLifecycleSupersedesTerminalBootAttemptReasonWithoutMutatingAttemptAuthority(t *testing.T) {
	runtimeDir := t.TempDir()
	attemptStore, reasonStore := terminalBootAttemptForProductReasonTest(t, runtimeDir)
	inactive := api.StatusResponse{Connection: "inactive"}
	if got := resolveProductTerminalReason(inactive, reasonStore, attemptStore); got != api.TerminalReasonVPNConnectFailed {
		t.Fatalf("initial terminal boot reason = %q", got)
	}

	tracker := &productLifecyclePhaseTracker{}
	lifecycle := productPhaseLifecycle{
		inner:           productReasonSuccessfulLifecycle{},
		tracker:         tracker,
		terminalReasons: &reasonStore,
	}
	request := bootRequest(testBootAutostartConfig())
	if _, err := lifecycle.Connect(context.Background(), request); err != nil {
		t.Fatalf("manual connect failed: %v", err)
	}
	if _, err := lifecycle.Disconnect(context.Background()); err != nil {
		t.Fatalf("manual disconnect failed: %v", err)
	}

	attempt, exists, err := attemptStore.LoadCurrent()
	if err != nil || !exists || attempt.State != bootAutostartAttemptTerminal {
		t.Fatalf("manual lifecycle mutated boot authority: attempt=%+v exists=%v err=%v", attempt, exists, err)
	}
	if got := resolveProductTerminalReason(inactive, reasonStore, attemptStore); got != "" {
		t.Fatalf("old boot terminal reason resurfaced after manual connect/disconnect: %q", got)
	}
}

func TestManualLifecycleSupersedesRuntimeTerminalReason(t *testing.T) {
	runtimeDir := t.TempDir()
	reasonStore := newProductTerminalReasonStore(runtimeDir, fixedBootID(testBootAttempt))
	if err := reasonStore.Set(api.TerminalReasonVPNRestoreFailed); err != nil {
		t.Fatal(err)
	}
	attemptStore := newBootAutostartAttemptStore(runtimeDir, fixedBootID(testBootAttempt))
	inactive := api.StatusResponse{Connection: "inactive"}
	if got := resolveProductTerminalReason(inactive, reasonStore, attemptStore); got != api.TerminalReasonVPNRestoreFailed {
		t.Fatalf("initial runtime terminal reason = %q", got)
	}

	lifecycle := productPhaseLifecycle{
		inner:           productReasonSuccessfulLifecycle{},
		tracker:         &productLifecyclePhaseTracker{},
		terminalReasons: &reasonStore,
	}
	request := bootRequest(testBootAutostartConfig())
	if _, err := lifecycle.Connect(context.Background(), request); err != nil {
		t.Fatalf("manual connect failed: %v", err)
	}
	if _, err := lifecycle.Disconnect(context.Background()); err != nil {
		t.Fatalf("manual disconnect failed: %v", err)
	}

	if got := resolveProductTerminalReason(inactive, reasonStore, attemptStore); got != "" {
		t.Fatalf("runtime terminal reason resurfaced after manual connect/disconnect: %q", got)
	}
}
