package client

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	podlogs "github.com/AidarKhusainov/podlaz/internal/logs"
)

func TestIssue254LogsClientStreamsDaemonResponseOverUnixSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podlazd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != api.LogsPath {
			t.Errorf("path=%q", r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("since") != "36h" || query.Get("follow") != "1" || query.Get("core") != "1" {
			t.Errorf("query=%q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte("podlaz core logs\nfixture line\n"))
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	var stdout bytes.Buffer
	err = (LogsClient{SocketPath: socketPath, DialTimeout: time.Second}).Run(
		context.Background(),
		&stdout,
		podlogs.Options{Since: "036h", Follow: true, Core: true},
	)
	if err != nil {
		t.Fatalf("logs request: %v", err)
	}
	if stdout.String() != "podlaz core logs\nfixture line\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestIssue254LogsClientReturnsLateBackendFailure(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podlazd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Trailer", api.LogsErrorTrailer)
		_, _ = w.Write([]byte("podlaz daemon logs\n"))
		w.Header().Set(api.LogsErrorTrailer, "backend-failed")
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	var stdout bytes.Buffer
	err = (LogsClient{SocketPath: socketPath, DialTimeout: time.Second}).Run(context.Background(), &stdout, podlogs.Options{})
	if err == nil || err.Error() != "daemon logs backend failed" {
		t.Fatalf("error=%v, want daemon logs backend failure", err)
	}
	if stdout.String() != "podlaz daemon logs\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestIssue254LogsClientPreservesFollowCancellation(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podlazd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	started := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-r.Context().Done()
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (LogsClient{SocketPath: socketPath, DialTimeout: time.Second}).Run(ctx, &bytes.Buffer{}, podlogs.Options{Follow: true})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("follow request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("follow cancellation error=%v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("follow request did not return after cancellation")
	}
}

func TestIssue254LogsAbstractFallbackMessageDoesNotClaimPolkitAuthorization(t *testing.T) {
	filesystemErr := daemonUnavailableError{
		detail:           "filesystem permission denied",
		cause:            os.ErrPermission,
		permissionDenied: true,
	}
	abstractErr := daemonUnavailableError{
		detail: "abstract socket refused",
		cause:  syscall.ECONNREFUSED,
	}

	err := logsAbstractSocketFallbackError(filesystemErr, abstractErr)
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Fatalf("fallback error=%v, want ErrDaemonUnavailable", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "polkit") {
		t.Fatalf("read-only logs fallback must not claim polkit authorization: %v", err)
	}
}
