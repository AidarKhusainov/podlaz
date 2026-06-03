package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	"github.com/AidarKhusainov/tunwarden/internal/client"
)

func TestServerExposesStatusOverUnixSocket(t *testing.T) {
	runtimeDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (Server{RuntimeDir: runtimeDir}).Run(ctx) }()

	statusClient := client.StatusClient{SocketPath: runtimeDir + "/tunwardend.sock", Timeout: time.Second}
	var daemon string
	var service string
	for i := 0; i < 50; i++ {
		status, err := statusClient.Status(context.Background())
		if err == nil {
			daemon = status.Daemon
			service = status.Service
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("server shutdown failed: %v", err)
	}
	if daemon != "running" {
		t.Fatalf("expected daemon running status, got %q", daemon)
	}
	if service != api.ServiceManual {
		t.Fatalf("expected manual service status outside systemd, got %q", service)
	}
}

func TestDefaultStatusReportsSystemdServiceFromEnvironment(t *testing.T) {
	t.Setenv(api.ServiceEnv, api.ServiceSystemd)

	status := DefaultStatus(context.Background())

	if status.Service != api.ServiceSystemd {
		t.Fatalf("expected %q service, got %q", api.ServiceSystemd, status.Service)
	}
}

func TestServerExposesDoctorOverUnixSocket(t *testing.T) {
	runtimeDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (Server{RuntimeDir: runtimeDir}).Run(ctx) }()

	doctorClient := client.DoctorClient{SocketPath: runtimeDir + "/tunwardend.sock", Timeout: time.Second}
	var source string
	var sawDaemonCheck bool
	for i := 0; i < 50; i++ {
		report, err := doctorClient.Doctor(context.Background())
		if err == nil {
			source = report.Source
			for _, check := range report.Checks {
				if check.Name == "daemon" && check.Severity == "OK" && check.Message == "running" {
					sawDaemonCheck = true
				}
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("server shutdown failed: %v", err)
	}
	if source != "daemon" {
		t.Fatalf("expected daemon doctor source, got %q", source)
	}
	if !sawDaemonCheck {
		t.Fatalf("expected daemon doctor check")
	}
}

func TestServerRejectsNonSocketAtSocketPath(t *testing.T) {
	runtimeDir := t.TempDir()
	socketPath := filepath.Join(runtimeDir, api.SocketName)
	if err := os.WriteFile(socketPath, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := (Server{RuntimeDir: runtimeDir}).Run(context.Background())
	if err == nil {
		t.Fatal("expected non-socket path to fail startup")
	}
	if !strings.Contains(err.Error(), "exists and is not a Unix socket") {
		t.Fatalf("expected explicit non-socket error, got %v", err)
	}
	if _, statErr := os.Stat(socketPath); statErr != nil {
		t.Fatalf("non-socket path must not be removed, stat error: %v", statErr)
	}
}
