package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestStatusReturnsUnavailableWhenSocketMissing(t *testing.T) {
	_, err := (StatusClient{SocketPath: filepath.Join(t.TempDir(), "missing.sock")}).Status(context.Background())
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

func TestUnavailableMessagePreservesPermissionDeniedGuidance(t *testing.T) {
	cause := &os.PathError{Op: "connect", Path: "/run/podlaz/podlazd.sock", Err: os.ErrPermission}
	err := daemonUnavailableError{
		detail:           unavailableDetail("/run/podlaz/podlazd.sock", cause),
		cause:            cause,
		permissionDenied: isPermissionDenied(cause),
	}
	if !IsDaemonUnavailable(err) {
		t.Fatalf("expected daemon unavailable classification, got %v", err)
	}
	if !IsDaemonPermissionDenied(err) {
		t.Fatalf("expected permission denied classification, got %v", err)
	}
	got := UnavailableMessage(err)
	if !strings.Contains(got, "permission denied") || !strings.Contains(got, "polkit-gated abstract socket") {
		t.Fatalf("expected packaged polkit guidance, got %q", got)
	}
	if strings.Contains(got, "usermod") || strings.Contains(got, "newgrp") || strings.Contains(got, "podlaz group") {
		t.Fatalf("primary guidance must not require manual group setup, got %q", got)
	}
	if strings.Contains(got, ErrDaemonUnavailable.Error()) {
		t.Fatalf("user-facing message should not include sentinel error text, got %q", got)
	}
}

func TestUnavailableMessageExplainsUnavailablePolkitFallback(t *testing.T) {
	filesystemCause := &os.PathError{Op: "connect", Path: "/run/podlaz/podlazd.sock", Err: os.ErrPermission}
	filesystemErr := daemonUnavailableError{
		detail:           unavailableDetail("/run/podlaz/podlazd.sock", filesystemCause),
		cause:            filesystemCause,
		permissionDenied: true,
	}
	abstractErr := daemonUnavailableError{
		detail: unavailableDetail(api.AbstractSocketAddress(), syscall.ECONNREFUSED),
		cause:  syscall.ECONNREFUSED,
	}

	err := abstractSocketFallbackError(filesystemErr, abstractErr)
	if !IsDaemonUnavailable(err) {
		t.Fatalf("expected daemon unavailable classification, got %T: %v", err, err)
	}
	if !IsDaemonPermissionDenied(err) {
		t.Fatalf("expected original filesystem permission-denied classification, got %T: %v", err, err)
	}
	got := UnavailableMessage(err)
	for _, want := range []string{
		"Authentication service is unavailable",
		"polkit IPC path",
		"plz doctor",
		"PODLAZ_SERVICE=systemd",
		"PODLAZ_POLKIT_AUTHORIZATION=required",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in fallback guidance, got %q", want, got)
		}
	}
	for _, forbidden := range []string{
		"daemon socket podlazd refused the connection",
		"\x00",
		"usermod",
		"newgrp",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("fallback guidance contains misleading or manual setup text %q: %q", forbidden, got)
		}
	}
}

func TestAbstractFallbackPreservesReachableDaemonProtocolError(t *testing.T) {
	filesystemCause := &os.PathError{Op: "connect", Path: "/run/podlaz/podlazd.sock", Err: os.ErrPermission}
	filesystemErr := daemonUnavailableError{detail: unavailableDetail("/run/podlaz/podlazd.sock", filesystemCause), cause: filesystemCause, permissionDenied: true}
	protocolErr := errors.New("daemon status response was invalid: missing daemon field")

	err := abstractSocketFallbackError(filesystemErr, protocolErr)
	if !errors.Is(err, protocolErr) {
		t.Fatalf("reachable daemon protocol errors must be authoritative, got %v", err)
	}
	if IsDaemonUnavailable(err) {
		t.Fatalf("protocol errors must not be downgraded to daemon unavailable: %v", err)
	}
}

func TestStatusRejectsIncompleteDaemonResponse(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "podlazd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		_ = server.Close()
		<-done
	}()

	_, err = (StatusClient{SocketPath: socketPath}).Status(context.Background())
	if err == nil {
		t.Fatal("expected incomplete daemon response to fail")
	}
	if strings.Contains(err.Error(), "podlazd unavailable") {
		t.Fatalf("protocol errors must not be classified as daemon unavailable: %v", err)
	}
	if !strings.Contains(err.Error(), "missing daemon field") {
		t.Fatalf("expected missing field validation error, got %v", err)
	}
}
