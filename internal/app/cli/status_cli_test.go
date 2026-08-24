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

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/client"
	"github.com/AidarKhusainov/podlaz/internal/status"
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
	if got != "Status: Disconnected\n" {
		t.Fatalf("unexpected concise daemon status: %q", got)
	}
	for _, forbidden := range []string{"Daemon:", "Service:", "Runtime directory:", "Routes:", "DNS:", "Firewall:"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("product status leaked %q: %q", forbidden, got)
		}
	}
}

func TestRunCLIStatusReportsMissingDaemonSocketAsUnknown(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "missing")
	t.Setenv(api.RuntimeDirEnv, runtimeDir)

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"status"}, &out, options{})
	if err == nil || ExitCode(err) != 3 {
		t.Fatalf("missing daemon must be unknown with exit 3, err=%v code=%d", err, ExitCode(err))
	}
	got := out.String()
	if !strings.Contains(got, "Status: Unknown\n") || !strings.Contains(got, "Reason: Connection state could not be determined\n") {
		t.Fatalf("missing daemon did not render safe unknown state: %q", got)
	}
	if strings.Contains(got, "Status: Disconnected") {
		t.Fatalf("missing daemon falsely claimed disconnect: %q", got)
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
	if !strings.Contains(got, "Status: Unknown\n") || !strings.Contains(got, "Reason: Connection state could not be determined\n") {
		t.Fatalf("permission-denied status did not render unknown: %q", got)
	}
	if strings.Contains(got, "Recovery candidates:") || strings.Contains(got, "generated runtime configs") || strings.Contains(got, "Status: Disconnected") {
		t.Fatalf("permission-denied product status leaked diagnostics or claimed disconnect: %q", got)
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
