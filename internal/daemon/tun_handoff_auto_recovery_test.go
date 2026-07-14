package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func TestAutoRecoverTunOwnedStateRecoversForDefaultPolicy(t *testing.T) {
	originalRecover := automaticPodlazRecover
	recoverCalls := 0
	automaticPodlazRecover = func(context.Context, string) error {
		recoverCalls++
		return nil
	}
	t.Cleanup(func() { automaticPodlazRecover = originalRecover })

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
		t.Fatalf("refreshed snapshot must be clean after successful automatic recovery: %#v", recovered.StaleResources)
	}
	if recoverCalls != 1 {
		t.Fatalf("automatic recovery calls = %d, want 1", recoverCalls)
	}
	if refreshCalls != 1 {
		t.Fatalf("snapshot refresh calls = %d, want 1", refreshCalls)
	}
}

func TestAutoRecoverTunOwnedStateKeepsAskMutationFree(t *testing.T) {
	originalRecover := automaticPodlazRecover
	recoverCalls := 0
	automaticPodlazRecover = func(context.Context, string) error {
		recoverCalls++
		return nil
	}
	t.Cleanup(func() { automaticPodlazRecover = originalRecover })

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
	originalRecover := automaticPodlazRecover
	automaticPodlazRecover = func(context.Context, string) error {
		return errors.New("automatic recovery failed")
	}
	t.Cleanup(func() { automaticPodlazRecover = originalRecover })

	manager := &XrayManager{RuntimeDir: t.TempDir()}
	refreshCalls := 0
	manager.snapshotCollector = func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
		refreshCalls++
		return netsnapshot.FakeResolvedDesktop()
	}

	_, err := manager.autoRecoverTunOwnedState(context.Background(), netsnapshot.FakeDesktopWithStalepodlazResources(), api.HandoffBlock, netsnapshot.Options{})
	if err == nil || !strings.Contains(err.Error(), "automatic recovery failed") {
		t.Fatalf("expected automatic recovery failure, got %v", err)
	}
	if refreshCalls != 0 {
		t.Fatalf("failed recovery must not pretend to validate a refreshed snapshot, calls=%d", refreshCalls)
	}
}

func TestAutomaticRecoveryCompleteAllowsOnlyDeferredResolvedRecord(t *testing.T) {
	result := recovery.ExecuteResult{Results: []recovery.CleanupResult{
		{Candidate: recovery.Candidate{Kind: "route-table"}, Status: "recovered"},
		{
			Candidate: recovery.Candidate{Kind: "dns-link"},
			Status:    "skipped",
			Message:   "systemd-resolved link record persisted after revert; restart systemd-resolved manually",
		},
	}}
	if !automaticRecoveryComplete(result) {
		t.Fatal("persistent podlaz resolved record must be deferred until podlaz0 is recreated")
	}
}

func TestAutomaticRecoveryCompleteRejectsOtherIncompleteCleanup(t *testing.T) {
	cases := []recovery.ExecuteResult{
		{Warnings: []recovery.Warning{{Target: "transaction state", Message: "inspection failed"}}},
		{Results: []recovery.CleanupResult{{Candidate: recovery.Candidate{Kind: "transaction-state"}, Status: "skipped", Message: "transaction state was preserved"}}},
		{Results: []recovery.CleanupResult{{Candidate: recovery.Candidate{Kind: "dns-link"}, Status: "failed", Message: "unexpected resolver failure"}}},
	}
	for i, result := range cases {
		if automaticRecoveryComplete(result) {
			t.Fatalf("case %d: incomplete or failed recovery must remain blocking", i)
		}
	}
}
