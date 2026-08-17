package client

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"path/filepath"
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
