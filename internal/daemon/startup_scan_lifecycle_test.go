package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
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

func TestStartupScanRefreshingLifecycleUsesBoundedAuthoritativeRefresh(t *testing.T) {
	manager := NewXrayManager(t.TempDir())
	var deadlinePresent bool
	lifecycle := startupScanRefreshingLifecycle{
		lifecycle: manager,
		refresh: func(ctx context.Context) {
			_, deadlinePresent = ctx.Deadline()
		},
	}
	if _, err := lifecycle.Connect(context.Background(), api.ConnectRequest{Mode: "unsupported"}); err == nil {
		t.Fatal("expected failed connect")
	}
	if !deadlinePresent {
		t.Fatal("post-lifecycle authoritative refresh must be bounded")
	}
}

func TestStartupScanRefreshingLifecycleDisconnectPublishesFreshPostMutationState(t *testing.T) {
	runtimeDir := t.TempDir()
	orderPath := filepath.Join(runtimeDir, "disconnect-order.log")
	configPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("create generated dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatalf("write generated config: %v", err)
	}

	plan := transactionPlanForTest()
	plan.TunDevice.Action = "verify"
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: fixedClock()}
	tx := txstate.NewTransaction("tun-post-disconnect-refresh", "test-profile", planner.ModeTun, store.Now())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan = desiredPlanFromTunPlan(plan)
	tx.Rollback = rollbackMetadataFromTunPlan(plan)
	tx.AppliedSteps = appliedStepsFromRollbackMetadataForTest(tx.Rollback, store.Now())
	tx.Rollback.GeneratedConfigs = append(tx.Rollback.GeneratedConfigs, txstate.GeneratedConfigRollback{Path: configPath, Owner: txstate.TransactionOwner})
	if _, err := store.Save(tx); err != nil {
		t.Fatalf("save active TUN transaction: %v", err)
	}

	fakeXray := writeFakeXray(t, `#!/bin/sh
trap 'printf "%s\n" stop-xray >> "$ORDER_FILE"; exit 0' TERM
while true; do
  sleep 3600 &
  wait $!
done
`)
	cmd := exec.Command(fakeXray)
	cmd.Env = append(os.Environ(), "ORDER_FILE="+orderPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake Xray: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
	})

	manager := &XrayManager{
		RuntimeDir:  runtimeDir,
		StopTimeout: 3 * time.Second,
		tunExecutor: &activeTunDisconnectOrderExecutor{orderPath: orderPath},
	}
	manager.mu.Lock()
	manager.cmd = cmd
	manager.done = done
	manager.state = xrayState{
		Connection:        "active",
		Mode:              planner.ModeTun,
		ProfileID:         tx.ProfileID,
		RuntimeConfigPath: configPath,
		TransactionID:     tx.ID,
	}
	manager.mu.Unlock()

	postMutationObserved := false
	scanState := newStartupScanState(func(context.Context) recovery.PlanResult {
		if _, _, err := store.Load(tx.ID); err == nil {
			t.Error("authoritative disconnect refresh ran before transaction finalization")
		}
		if _, err := os.Stat(configPath); !os.IsNotExist(err) {
			t.Errorf("authoritative disconnect refresh ran before generated-config cleanup: %v", err)
		}
		select {
		case <-done:
		default:
			t.Error("authoritative disconnect refresh ran before Xray process quiescence")
		}
		postMutationObserved = true
		return recovery.PlanResult{}
	})
	scanState.mu.Lock()
	scanState.scan = recovery.PlanResult{Candidates: []recovery.Candidate{{Kind: "dns-link", Description: "previous DNS recovery evidence", Target: "podlaz0"}}}
	scanState.mu.Unlock()

	lifecycle := startupScanRefreshingLifecycle{
		lifecycle: manager,
		refresh: func(ctx context.Context) {
			scanState.ForceRefresh(ctx)
		},
	}
	disconnected, err := lifecycle.Disconnect(context.Background())
	if err != nil {
		t.Fatalf("disconnect active TUN: %v", err)
	}
	if disconnected.Connection != "inactive" {
		t.Fatalf("expected inactive disconnect response, got %#v", disconnected)
	}
	if !postMutationObserved {
		t.Fatal("disconnect did not execute the authoritative post-mutation refresh")
	}
	fresh := scanState.Snapshot()
	if len(fresh.Candidates) != 0 || len(fresh.Warnings) != 0 {
		t.Fatalf("post-disconnect force refresh retained stale publication: %#v", fresh)
	}
}
