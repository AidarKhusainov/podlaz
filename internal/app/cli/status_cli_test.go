package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	"github.com/AidarKhusainov/tunwarden/internal/client"
	"github.com/AidarKhusainov/tunwarden/internal/status"
)

func TestRunCLIStatusUsesAccessibleDaemonSocket(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	t.Setenv(api.RuntimeDirEnv, runtimeDir)

	socketPath := api.SocketPath(runtimeDir)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on fake daemon socket: %v", err)
	}
	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != api.StatusPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.StatusResponse{
			Daemon:           "running",
			Service:          api.ServiceManual,
			Connection:       "inactive",
			RuntimeDirectory: "present",
			Proxy:            "inactive",
			TUN:              "disabled",
			Routes:           "not modified",
			DNS:              "not modified",
			Firewall:         "not modified",
		})
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		_ = server.Shutdown(context.Background())
		if err := <-done; err != nil && err != http.ErrServerClosed {
			t.Fatalf("fake daemon shutdown failed: %v", err)
		}
	}()

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"status"}, &out, options{}); err != nil {
		t.Fatalf("status failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Daemon: running\n",
		"Service: manual\n",
		"Connection: inactive\n",
		"Runtime directory: present\n",
		"Stale state: none\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "using local fallback") || strings.Contains(got, "Recovery candidates:") {
		t.Fatalf("accessible daemon status should not use local fallback, got %q", got)
	}
}

func TestRunCLIStatusReportsMissingDaemonSocketWithoutStaleState(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "missing")
	t.Setenv(api.RuntimeDirEnv, runtimeDir)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"status"}, &out, options{}); err != nil {
		t.Fatalf("missing daemon socket with no runtime state should be clean inactive, got %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Daemon: not reachable (daemon socket " + api.SocketPath(runtimeDir) + " does not exist; start tunwardend); using local fallback\n",
		"Daemon socket: missing\n",
		"Runtime directory: missing\n",
		"Stale state: none\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got %q", want, got)
		}
	}
}

func TestRunCLIStatusReportsPermissionDeniedDaemonSocketWithoutStaleRuntimeCandidate(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	if err := os.MkdirAll(filepath.Join(runtimeDir, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	socketPath := api.SocketPath(runtimeDir)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv(api.RuntimeDirEnv, runtimeDir)

	var out bytes.Buffer
	err = runWithOptions(context.Background(), []string{"status"}, &out, options{
		daemonStatus: func(context.Context) (status.Report, error) {
			return status.Report{}, fmt.Errorf("%w: %w", client.ErrDaemonUnavailable, client.ErrDaemonPermissionDenied)
		},
	})
	if err == nil || ExitCode(err) != 3 {
		t.Fatalf("expected diagnostic exit 3 for inaccessible daemon socket, got err=%v code=%d", err, ExitCode(err))
	}
	got := out.String()
	for _, want := range []string{
		"Daemon socket: present but inaccessible (permission denied; check tunwarden group membership)\n",
		"Runtime directory: present (daemon socket inaccessible; stale status unknown)\n",
		"Stale state: unknown (inspection incomplete)\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "Recovery candidates:") || strings.Contains(got, "generated runtime configs") || strings.Contains(got, "runtime directory: "+runtimeDir) {
		t.Fatalf("permission-denied daemon runtime should not be reported as stale cleanup candidates, got %q", got)
	}
}

func shortRuntimeDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tw-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
