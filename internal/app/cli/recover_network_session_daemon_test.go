package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestRecoverDryRunUsesDaemonNetworkSessionPlanWithoutTransactionCandidates(t *testing.T) {
	state := failedResumeRecoveryStateForCLI()
	runtimeDir, shutdown := serveRecoverContractDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != api.StatusPath {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.StatusResponse{
			Daemon:           "running",
			Service:          api.ServiceManual,
			Connection:       "inactive",
			RuntimeDirectory: "present",
			Proxy:            "inactive",
			TUN:              "disabled",
			StartupScan: &api.StartupScanStatus{
				Status:          api.StartupScanStatusStale,
				NetworkSession:  state,
				SuggestedAction: "podlaz recover",
			},
		})
	})
	defer shutdown()
	t.Setenv(api.RuntimeDirEnv, runtimeDir)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"recover"}, &out, options{}); err != nil {
		t.Fatalf("recover dry-run: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "No podlaz-owned recovery candidates found") {
		t.Fatalf("retained session was hidden as no candidates: %q", got)
	}
	for _, want := range []string{"Network Session authority: present", "Resume stage: connect-replay", "Next action: retry-resume"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dry-run missing %q: %q", want, got)
		}
	}
}

func TestRecoverExecuteUsesSameFailedNetworkSessionOutcomeAndExitCode(t *testing.T) {
	state := failedResumeRecoveryStateForCLI()
	runtimeDir, shutdown := serveRecoverContractDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != api.RecoverPath {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(api.RecoveryResponse{Mode: "execute", NetworkSession: state})
	})
	defer shutdown()
	t.Setenv(api.RuntimeDirEnv, runtimeDir)

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"recover", "--execute", "--yes"}, &out, options{})
	if got := ExitCode(err); got != 1 {
		t.Fatalf("execute exit=%d err=%v, want 1", got, err)
	}
	for _, want := range []string{"Network Session authority: present", "Resume stage: connect-replay", "Next action: retry-resume"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("execute missing %q: %q", want, out.String())
		}
	}
}

func failedResumeRecoveryStateForCLI() *api.NetworkSessionRecoveryState {
	return &api.NetworkSessionRecoveryState{
		Authority:           api.NetworkSessionRecoveryAuthorityPresent,
		Intent:              "resume",
		StartupGate:         api.NetworkSessionStartupGateBlocked,
		ResumeStage:         api.NetworkSessionResumeStageConnectReplay,
		LastResumeOutcome:   api.NetworkSessionResumeOutcomeFailed,
		LastTUNFailurePhase: "preflight",
		RollbackStatus:      "not-started",
		CleanupAuthority:    api.NetworkSessionCleanupAuthorityNone,
		NextAction:          api.NetworkSessionRecoveryActionRetryResume,
	}
}

func serveRecoverContractDaemon(t *testing.T, handler http.HandlerFunc) (string, func()) {
	t.Helper()
	runtimeDir := t.TempDir()
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", api.SocketPath(runtimeDir))
	if err != nil {
		t.Fatalf("listen on fake daemon socket: %v", err)
	}
	server := http.Server{Handler: handler}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	return runtimeDir, func() {
		_ = server.Shutdown(context.Background())
		if err := <-done; err != nil && err != http.ErrServerClosed {
			t.Fatalf("fake daemon shutdown failed: %v", err)
		}
	}
}
