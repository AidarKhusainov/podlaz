package e2e_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The installed CLI uses this one-shot relay to distinguish a verified
// daemon commit from a missing HTTP acknowledgment. Keep the relay itself
// executable and behavior-tested rather than relying only on marker matching.
func TestLifecycleResponseDropRelayClosesAfterConfirmedDaemonCommit(t *testing.T) {
	root := t.TempDir()
	daemonSocket := filepath.Join(root, "backend.sock")
	proxySocket := filepath.Join(root, "podlazd.sock")
	marker := filepath.Join(root, "committed")
	listener, err := net.Listen("unix", daemonSocket)
	if err != nil {
		t.Fatal(err)
	}
	var connections atomic.Int32
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"connection\":\"active\"}"))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/connect":
			_, _ = io.Copy(io.Discard, r.Body)
			connections.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"connection\":\"active\",\"mode\":\"tun\",\"proxy\":\"active\",\"tun\":\"active\"}"))
		default:
			http.Error(w, "unsupported", http.StatusMethodNotAllowed)
		}
	})}
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = server.Serve(listener)
	}()
	defer func() {
		_ = server.Close()
		<-serveDone
	}()

	cmd := exec.Command("python3", filepath.Join("lib", "lifecycle_response_drop.py"), proxySocket, daemonSocket, marker)
	var proxyErr bytes.Buffer
	cmd.Stderr = &proxyErr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start response-drop relay: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if info, err := os.Stat(proxySocket); err == nil && info.Mode()&os.ModeSocket != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("response-drop relay did not bind: %s", proxyErr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", proxySocket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}
	get, err := client.Get("http://podlazd/v1/status")
	if err != nil {
		t.Fatalf("read through installed-client relay: %v", err)
	}
	getBody, err := io.ReadAll(get.Body)
	_ = get.Body.Close()
	if err != nil || get.StatusCode != 200 || !strings.Contains(string(getBody), "\"active\"") {
		t.Fatalf("read-only status was not forwarded: code=%d body=%q err=%v", get.StatusCode, getBody, err)
	}

	post, err := client.Post("http://podlazd/v1/connect", "application/json",
		strings.NewReader("{\"mode\":\"tun\"}"))
	if post != nil {
		_ = post.Body.Close()
	}
	if err == nil {
		t.Fatal("a committed mutation unexpectedly returned a successful HTTP reply")
	}
	if waitErr := cmd.Wait(); waitErr != nil {
		t.Fatalf("one-shot response-drop relay failed: %v: %s", waitErr, proxyErr.String())
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("response drop replayed a mutation: count=%d", got)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "committed\n" {
		t.Fatalf("missing confirmed HTTP 200 commit witness: marker=%q err=%v", data, err)
	}
}
