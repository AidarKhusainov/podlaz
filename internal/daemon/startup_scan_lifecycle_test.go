package daemon

import (
	"context"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestStartupScanRefreshingLifecycleRefreshesAfterSuccessAndFailure(t *testing.T) {
	manager := NewXrayManager(t.TempDir())
	refreshes := 0
	lifecycle := startupScanRefreshingLifecycle{
		lifecycle: manager,
		refresh:   func(context.Context) { refreshes++ },
	}

	if _, err := lifecycle.Connect(context.Background(), api.ConnectRequest{Mode: "unsupported"}); err == nil {
		t.Fatal("expected failed connect")
	}
	if refreshes != 1 {
		t.Fatalf("expected refresh after failed connect, got %d", refreshes)
	}
	if _, err := lifecycle.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if refreshes != 2 {
		t.Fatalf("expected refresh after successful disconnect, got %d", refreshes)
	}
}
