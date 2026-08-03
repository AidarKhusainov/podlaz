package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestRunCLIRecoverIncludesDaemonStartupScan(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv(api.RuntimeDirEnv, runtimeDir)
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}

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
			StartupScan: &api.StartupScanStatus{
				Status: api.StartupScanStatusStale,
				Candidates: []api.RecoveryCandidate{{
					Kind:        "transaction-state",
					Description: "transaction rollback state",
					Target:      filepath.Join(runtimeDir, "transactions", "tx-startup.json"),
					Transaction: &api.RecoveryTransactionCandidate{
						ID:                "tx-startup",
						State:             "applying",
						Status:            "pending apply",
						RollbackAvailable: true,
						RequiresCleanup:   true,
						Path:              filepath.Join(runtimeDir, "transactions", "tx-startup.json"),
					},
				}},
				SuggestedAction: "podlaz recover",
			},
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
	if err := runWithOptions(context.Background(), []string{"recover"}, &out, options{}); err != nil {
		t.Fatalf("recover failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"podlaz recovery dry-run",
		"Transaction: pending apply",
		"Rollback available: yes",
		"State path: " + filepath.Join(runtimeDir, "transactions", "tx-startup.json"),
		"No changes were applied.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got %q", want, got)
		}
	}
}

func TestRunCLIRecoverTreatsDaemonStartupScanAsAuthoritative(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv(api.RuntimeDirEnv, runtimeDir)
	generatedDir := filepath.Join(runtimeDir, "generated")
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		t.Fatal(err)
	}

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
			StartupScan: &api.StartupScanStatus{
				Status: api.StartupScanStatusClean,
			},
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
	if err := runWithOptions(context.Background(), []string{"recover"}, &out, options{}); err != nil {
		t.Fatalf("recover failed: %v", err)
	}
	got := out.String()
	if strings.Contains(got, generatedDir) {
		t.Fatalf("fresh daemon absence must supersede stale local candidate, got %q", got)
	}
	if !strings.Contains(got, "No podlaz-owned recovery candidates found.") {
		t.Fatalf("expected authoritative clean recovery result, got %q", got)
	}
}
