package client

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestDoctorResponseTimeoutAfterSuccessfulDaemonDialIsNotDaemonUnavailable(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podlazd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := http.Server{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		_ = server.Close()
		<-done
	}()

	_, err = (DoctorClient{SocketPath: socketPath, Timeout: 25 * time.Millisecond}).Doctor(context.Background())
	if err == nil {
		t.Fatal("expected bounded daemon doctor response timeout")
	}
	if IsDaemonUnavailable(err) {
		t.Fatalf("successful daemon dial followed by response timeout was misclassified as daemon unavailable: %v", err)
	}
}

func TestTunDoctorResponseTimeoutAfterSuccessfulDaemonDialIsNotDaemonUnavailable(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podlazd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := http.Server{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		_ = server.Close()
		<-done
	}()

	_, err = (DoctorClient{SocketPath: socketPath, Timeout: 25 * time.Millisecond}).TunDiagnostics(context.Background())
	if err == nil {
		t.Fatal("expected bounded daemon TUN diagnostic response timeout")
	}
	if IsDaemonUnavailable(err) {
		t.Fatalf("successful daemon dial followed by TUN diagnostic response timeout was misclassified as daemon unavailable: %v", err)
	}
}
