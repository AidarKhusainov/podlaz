package daemon

import (
	"context"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestPrepareTunHandoffAutoRecoversPodlazOwnedStateForDefaultPolicy(t *testing.T) {
	originalRecover := controlledPodlazRecover
	recoverCalls := 0
	controlledPodlazRecover = func(context.Context, string) error {
		recoverCalls++
		return nil
	}
	t.Cleanup(func() { controlledPodlazRecover = originalRecover })

	manager := &XrayManager{RuntimeDir: t.TempDir()}
	refreshCalls := 0
	manager.snapshotCollector = func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
		refreshCalls++
		return netsnapshot.FakeResolvedDesktop()
	}

	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeDesktopWithStalepodlazResources(), api.HandoffBlock, netsnapshot.Options{})
	if err != nil {
		t.Fatalf("default TUN connect must auto-recover unambiguous podlaz-owned stale state: %v", err)
	}
	if recoverCalls != 1 {
		t.Fatalf("controlled recovery calls = %d, want 1", recoverCalls)
	}
	if refreshCalls != 1 {
		t.Fatalf("snapshot refresh calls = %d, want 1", refreshCalls)
	}
}
