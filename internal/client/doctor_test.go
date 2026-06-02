package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorReturnsUnavailableWhenSocketMissing(t *testing.T) {
	_, err := (DoctorClient{SocketPath: filepath.Join(t.TempDir(), "missing.sock")}).Doctor(context.Background())
	if err == nil {
		t.Fatal("expected missing socket to fail")
	}
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("expected ErrDaemonUnavailable, got %v", err)
	}
	if got := UnavailableMessage(err); got == "" {
		t.Fatal("expected actionable unavailable message")
	}
}

func TestDoctorRejectsIncompleteDaemonResponse(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "tunwardend.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		_ = server.Close()
		<-done
	}()

	_, err = (DoctorClient{SocketPath: socketPath}).Doctor(context.Background())
	if err == nil {
		t.Fatal("expected incomplete daemon response to fail")
	}
	if strings.Contains(err.Error(), "tunwardend unavailable") {
		t.Fatalf("protocol errors must not be classified as daemon unavailable: %v", err)
	}
	if !strings.Contains(err.Error(), "missing source field") {
		t.Fatalf("expected missing field validation error, got %v", err)
	}
}

func TestDoctorReadsDaemonResponse(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "tunwardend.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"source\":\"daemon\",\"checks\":[{\"name\":\"daemon\",\"severity\":\"OK\",\"message\":\"running\"}]}"))
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		_ = server.Close()
		<-done
	}()

	report, err := (DoctorClient{SocketPath: socketPath}).Doctor(context.Background())
	if err != nil {
		t.Fatalf("doctor request failed: %v", err)
	}
	if report.Source != "daemon" {
		t.Fatalf("expected daemon source, got %q", report.Source)
	}
	if len(report.Checks) != 1 || report.Checks[0].Name != "daemon" {
		t.Fatalf("expected daemon check, got %#v", report.Checks)
	}
}
