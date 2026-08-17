package client

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

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
		if r.URL.Path != "/v1/logs" {
			t.Errorf("path=%q", r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("since") != "15m" || query.Get("follow") != "1" || query.Get("core") != "1" {
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
		podlogs.Options{Since: "015m", Follow: true, Core: true},
	)
	if err != nil {
		t.Fatalf("logs request: %v", err)
	}
	if stdout.String() != "podlaz core logs\nfixture line\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestIssue254LogsClientClassifiesDaemonGroupDenial(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podlazd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "daemon logs access denied", http.StatusForbidden)
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	err = (LogsClient{SocketPath: socketPath, DialTimeout: time.Second}).Run(context.Background(), &bytes.Buffer{}, podlogs.Options{})
	if !errors.Is(err, ErrDaemonPermissionDenied) {
		t.Fatalf("error=%v, want ErrDaemonPermissionDenied", err)
	}
}
