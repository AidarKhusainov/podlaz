package client

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
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
