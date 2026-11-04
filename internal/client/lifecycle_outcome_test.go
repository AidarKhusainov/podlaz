package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func lifecycleTestRequest() api.ConnectRequest {
	return api.ConnectRequest{
		Mode: "tun",
		Profile: api.ProfileSnapshot{
			ID: "test-profile", Name: "Test profile",
			Server: "vpn.example.test", Port: 443, Protocol: "vless",
		},
		Handoff: api.HandoffBlock,
	}
}

func serveLifecycleTestSocket(t *testing.T, handler http.Handler) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "podlazd.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		<-done
	})
	return path
}

func TestLifecycleResponseTimeoutAfterDialIsUnknownNotDaemonUnavailable(t *testing.T) {
	var mutations atomic.Int32
	socket := serveLifecycleTestSocket(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		mutations.Add(1)
		<-r.Context().Done()
	}))
	client := LifecycleClient{SocketPath: socket, Timeout: 30 * time.Millisecond}
	_, err := client.Connect(context.Background(), lifecycleTestRequest())
	if !errors.Is(err, ErrLifecycleOutcomeUnknown) || IsDaemonUnavailable(err) {
		t.Fatalf("post-dial response timeout was not an unknown lifecycle outcome: %v", err)
	}
	if got := mutations.Load(); got != 1 {
		t.Fatalf("timed-out connect automatically retried: mutation count=%d", got)
	}
	if !strings.Contains(err.Error(), "podlaz status") {
		t.Fatalf("uncertain operation lacks actionable guidance: %v", err)
	}
}

func TestLifecycleLostResponseAfterCommitNeverRetriesMutation(t *testing.T) {
	var mutations atomic.Int32
	socket := serveLifecycleTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mutations.Add(1)
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("test daemon response cannot be hijacked")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	_, err := (LifecycleClient{SocketPath: socket}).Connect(context.Background(), lifecycleTestRequest())
	if !errors.Is(err, ErrLifecycleOutcomeUnknown) || IsDaemonUnavailable(err) {
		t.Fatalf("lost response after commit was not classified as unknown: %v", err)
	}
	if got := mutations.Load(); got != 1 {
		t.Fatalf("lost-response request was replayed: count=%d", got)
	}
}

func TestLifecycleAuthoritativeHTTPConflictIsNotUnknown(t *testing.T) {
	socket := serveLifecycleTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "protected replacement requires handoff", http.StatusConflict)
	}))
	_, err := (LifecycleClient{SocketPath: socket}).Connect(context.Background(), lifecycleTestRequest())
	if err == nil || !strings.Contains(err.Error(), "protected replacement requires handoff") ||
		errors.Is(err, ErrLifecycleOutcomeUnknown) || IsDaemonUnavailable(err) {
		t.Fatalf("authoritative daemon 409 was misclassified: %v", err)
	}
}

func TestLifecycleMalformedSuccessDoesNotConfirmConnected(t *testing.T) {
	socket := serveLifecycleTestSocket(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"connection\":\"active\"}"))
	}))
	_, err := (LifecycleClient{SocketPath: socket}).Connect(context.Background(), lifecycleTestRequest())
	if !errors.Is(err, ErrLifecycleOutcomeUnknown) {
		t.Fatalf("invalid success response must not confirm mutation result: %v", err)
	}
}

func TestDefaultLifecycleBudgetCoversVerificationAndRestore(t *testing.T) {
	if defaultNetworkLifecycleTimeout < 3*time.Minute {
		t.Fatalf("default lifecycle budget %s cannot cover two connectivity verification passes plus protected rollback/restore", defaultNetworkLifecycleTimeout)
	}
}
