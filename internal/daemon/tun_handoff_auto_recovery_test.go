package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestAutoRecoverTunOwnedStateRecoversForDefaultPolicy(t *testing.T) {
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

	recovered, err := manager.autoRecoverTunOwnedState(context.Background(), netsnapshot.FakeDesktopWithStalepodlazResources(), api.HandoffBlock, netsnapshot.Options{})
	if err != nil {
		t.Fatalf("default TUN connect must auto-recover unambiguous podlaz-owned stale state: %v", err)
	}
	if stalePodlazStateBlocker(recovered) != nil {
		t.Fatalf("refreshed snapshot must be clean after successful controlled recovery: %#v", recovered.StaleResources)
	}
	if recoverCalls != 1 {
		t.Fatalf("controlled recovery calls = %d, want 1", recoverCalls)
	}
	if refreshCalls != 1 {
		t.Fatalf("snapshot refresh calls = %d, want 1", refreshCalls)
	}
}

func TestAutoRecoverTunOwnedStateKeepsAskMutationFree(t *testing.T) {
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
	stale := netsnapshot.FakeDesktopWithStalepodlazResources()

	got, err := manager.autoRecoverTunOwnedState(context.Background(), stale, api.HandoffAsk, netsnapshot.Options{})
	if err != nil {
		t.Fatalf("ask policy inspection: %v", err)
	}
	if recoverCalls != 0 || refreshCalls != 0 {
		t.Fatalf("ask policy must not recover or refresh: recover=%d refresh=%d", recoverCalls, refreshCalls)
	}
	if stalePodlazStateBlocker(got) == nil {
		t.Fatal("ask policy must preserve stale evidence for the handoff blocker")
	}
}

func TestAutoRecoverTunOwnedStateStopsAfterRecoveryFailure(t *testing.T) {
	originalRecover := controlledPodlazRecover
	controlledPodlazRecover = func(context.Context, string) error {
		return errors.New("controlled recovery failed")
	}
	t.Cleanup(func() { controlledPodlazRecover = originalRecover })

	manager := &XrayManager{RuntimeDir: t.TempDir()}
	refreshCalls := 0
	manager.snapshotCollector = func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
		refreshCalls++
		return netsnapshot.FakeResolvedDesktop()
	}

	_, err := manager.autoRecoverTunOwnedState(context.Background(), netsnapshot.FakeDesktopWithStalepodlazResources(), api.HandoffBlock, netsnapshot.Options{})
	if err == nil || !strings.Contains(err.Error(), "controlled recovery failed") {
		t.Fatalf("expected controlled recovery failure, got %v", err)
	}
	if refreshCalls != 0 {
		t.Fatalf("failed recovery must not pretend to validate a refreshed snapshot, calls=%d", refreshCalls)
	}
}
