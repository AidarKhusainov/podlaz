package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestRecoverExecuteRoutesProtectionOnlyAuthorityThroughNetworkSessionResume(t *testing.T) {
	installRecoveryObservationFakes(t)

	runtimeDir := t.TempDir()
	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if err := continuation.Save(testContinuationRequest()); err != nil {
		t.Fatal(err)
	}
	if err := continuation.stateStore().SetProtection(&networkSessionProtection{
		State:              networkSessionProtectionArmed,
		CompositionVersion: privacyEnvelopeCompositionVersion,
		Family:             privacyEnvelopeFamily,
		Table:              "podlaz_pe_001122334455",
		TunInterface:       "podlaz0",
		BootstrapIPv4:      []string{"192.0.2.10"},
	}); err != nil {
		t.Fatal(err)
	}

	privacyReconciles := 0
	continuation.reconcilePrivacy = func(context.Context, networkSessionStateStore) error {
		privacyReconciles++
		return nil
	}
	continuation.recoverExact = func(context.Context, string) api.RecoveryResponse {
		return api.RecoveryResponse{Mode: "execute"}
	}

	events := []string{}
	sessionLifecycle := newNetworkSessionLifecycle(networkSessionRecordingLifecycle{events: &events}, continuation)
	gate := newNetworkSessionStartupMutationGate(sessionLifecycle)
	gate.Block()
	runtime := &daemonRuntime{
		runtimeDir: runtimeDir,
		authorizer: AllowAuthorizer{},
		currentStatus: func(context.Context) api.StatusResponse {
			return api.StatusResponse{Connection: "inactive"}
		},
		operationLock:           newLifecycleOperationLock(),
		continuation:            continuation,
		sessionLifecycle:        sessionLifecycle,
		startupMutationGate:     gate,
		forceRefreshStartupScan: func(context.Context) {},
	}
	manifestStore := newBootAutostartManifestStore(t.TempDir(), fixedBootID("boot-a"))
	httpServer := (Server{}).newHTTPServer(runtime, manifestStore)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, api.RecoverPath, nil)
	httpServer.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("recover status=%d body=%q", recorder.Code, recorder.Body.String())
	}

	var response api.RecoveryResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode recover response: %v", err)
	}
	if privacyReconciles != 1 {
		t.Fatalf("privacy reconciles=%d, want 1", privacyReconciles)
	}
	if !reflect.DeepEqual(events, []string{"connect"}) {
		t.Fatalf("resume events=%#v, want connect replay", events)
	}
	if gate.Blocked() {
		t.Fatal("successful protection-only recovery must release the startup mutation gate")
	}
	if response.NetworkSession == nil {
		t.Fatal("recover response lost Network Session recovery state")
	}
	if response.NetworkSession.CleanupAuthority != api.NetworkSessionCleanupAuthoritySessionProtection {
		t.Fatalf("cleanup authority=%q, want session protection", response.NetworkSession.CleanupAuthority)
	}
	if response.NetworkSession.TransactionPresent {
		t.Fatalf("protection-only recovery invented transaction authority: %#v", response.NetworkSession)
	}
	if response.NetworkSession.StartupGate != api.NetworkSessionStartupGateOpen ||
		response.NetworkSession.LastResumeOutcome != api.NetworkSessionResumeOutcomeSucceeded ||
		response.NetworkSession.NextAction != api.NetworkSessionRecoveryActionNone {
		t.Fatalf("unexpected successful recovery state: %#v", response.NetworkSession)
	}
	if len(response.Warnings) != 0 {
		t.Fatalf("successful protection-only recovery returned warnings: %#v", response.Warnings)
	}
}

func installRecoveryObservationFakes(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(binDir, name)
		content := "#!/bin/sh\nset -eu\n" + body + "\n"
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	write("ip", `printf '%s\n' 'Device "podlaz0" does not exist.' >&2
exit 1`)
	write("nft", `printf '%s\n' 'Error: Could not process rule: No such file or directory' >&2
exit 1`)
	write("resolvectl", `printf '%s\n' 'Failed to resolve interface "podlaz0": No such device' >&2
exit 1`)
	t.Setenv("PATH", binDir)
}
