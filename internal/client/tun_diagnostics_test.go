package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestTunDiagnosticsReadsVersionedDaemonResponse(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podlazd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != api.TunDoctorPath {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tundiag.Finalize(tundiag.Report{
			Session: tundiag.Session{State: "active", Mode: "tun"},
			Probes:  []tundiag.ProbeResult{{ID: "route-ipv4", Layer: tundiag.LayerRoute, Status: tundiag.ProbePass}},
		}))
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		_ = server.Close()
		<-done
	}()

	report, err := (DoctorClient{SocketPath: socketPath}).TunDiagnostics(context.Background())
	if err != nil {
		t.Fatalf("TUN diagnostics request failed: %v", err)
	}
	if report.SchemaVersion != tundiag.SchemaVersion || report.Status != tundiag.StatusHealthy {
		t.Fatalf("unexpected TUN diagnostics response: %#v", report)
	}
	if _, ok := report.Probe("route-ipv4"); !ok {
		t.Fatalf("missing route probe: %#v", report.Probes)
	}
}

func TestTunDiagnosticsRejectsUnsupportedSchema(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podlazd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":99,"status":"healthy","session":{},"network":{},"probes":[],"warnings":[],"errors":[]}`))
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		_ = server.Close()
		<-done
	}()

	_, err = (DoctorClient{SocketPath: socketPath}).TunDiagnostics(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unsupported schema_version 99") {
		t.Fatalf("expected unsupported schema error, got %v", err)
	}
}
