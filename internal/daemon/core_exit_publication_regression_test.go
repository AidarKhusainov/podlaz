package daemon

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/client"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func TestStatusForPublicationOmitsTransactionIDAfterCoreExit(t *testing.T) {
	manager := &XrayManager{}
	manager.state = xrayState{
		Connection:    "error (core exited)",
		Mode:          planner.ModeTun,
		TransactionID: "tun-crashed",
	}

	status := manager.statusForPublication(context.Background())
	if status.ActiveTransactionID != "" {
		t.Fatalf("error state must not publish an active transaction id: %#v", status)
	}
}

func TestCustomServerStatusOmitsTransactionIDOutsideActiveTun(t *testing.T) {
	t.Setenv(api.ServiceEnv, api.ServiceManual)
	runtimeDir := t.TempDir()
	configPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	manager := &XrayManager{RuntimeDir: runtimeDir}
	manager.state = xrayState{
		Connection:        "active",
		Mode:              planner.ModeTun,
		ProfileID:         "profile-test",
		RuntimeConfigPath: configPath,
		TransactionID:     "tun-active",
	}
	statusClient := startCoreExitTestServer(t, Server{
		RuntimeDir: runtimeDir,
		Lifecycle:  manager,
		Status: func(context.Context) api.StatusResponse {
			return api.StatusResponse{
				Daemon:            "running",
				Service:           api.ServiceManual,
				Connection:        "error (core exited)",
				Mode:              planner.ModeTun,
				ProfileID:         "profile-test",
				RuntimeDirectory:  "present",
				RuntimeConfigPath: configPath,
				Proxy:             "inactive",
				TUN:               "error",
			}
		},
		startupScan: func(context.Context) recovery.PlanResult { return recovery.PlanResult{} },
	})

	status, err := statusClient.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ActiveTransactionID != "" {
		t.Fatalf("custom error status must not publish an active transaction id: %#v", status)
	}

	// This test injects an impossible active TUN state without a transaction
	// solely to exercise publication. Restore a valid inactive lifecycle before
	// the server cleanup runs: shutdown errors are now intentionally propagated.
	manager.mu.Lock()
	manager.state = inactiveXrayState()
	manager.mu.Unlock()
}

func TestUnexpectedTunCoreExitRefreshesRecoverySnapshotWithBoundedContext(t *testing.T) {
	runtimeDir := t.TempDir()
	fakeXray := writeFakeXray(t, "#!/bin/sh\nsleep 0.5\nexit 23\n")
	manager := &XrayManager{RuntimeDir: runtimeDir, XrayPath: fakeXray, StopTimeout: time.Second}
	var scanCalls atomic.Int32
	var boundedRefresh atomic.Bool
	scanState := newStartupScanState(func(ctx context.Context) recovery.PlanResult {
		call := scanCalls.Add(1)
		if call <= 2 {
			return recovery.PlanResult{Candidates: []recovery.Candidate{{Kind: "tun-interface", Target: "podlaz0", Description: "stale TUN interface"}}}
		}
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			boundedRefresh.Store(remaining > 0 && remaining <= 6*time.Second)
		}
		return recovery.PlanResult{}
	})
	scanState.Refresh(context.Background())
	lifecycle := startupScanRefreshingLifecycle{
		lifecycle: manager,
		refresh: func(ctx context.Context) {
			scanState.Refresh(ctx)
		},
	}
	if _, err := lifecycle.Connect(context.Background(), connectRequestForTest()); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	done := manager.done
	manager.state.Mode = planner.ModeTun
	manager.state.TUN = "enabled (podlaz0)"
	manager.state.TransactionID = "tun-crashed"
	manager.state.RuntimeConfigPath = filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	manager.mu.Unlock()
	if done == nil {
		t.Fatal("connected core has no completion channel")
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("fake core did not exit")
	}
	deadline := time.Now().Add(2 * time.Second)
	for scanCalls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	status := manager.statusForPublication(context.Background())
	scan := scanState.FilterForStatus(status, runtimeDir)
	if scanCalls.Load() < 3 {
		t.Fatalf("unexpected core exit did not refresh recovery state; calls=%d scan=%#v", scanCalls.Load(), scan)
	}
	if !boundedRefresh.Load() {
		t.Fatal("unexpected-exit recovery refresh did not receive a bounded context")
	}
	if len(scan.Candidates) != 0 || len(scan.Warnings) != 0 {
		t.Fatalf("publication reused stale pre-exit recovery evidence: %#v", scan)
	}
	if status.ActiveTransactionID != "" {
		t.Fatalf("crashed TUN state must not publish an active transaction id: %#v", status)
	}
}

func startCoreExitTestServer(t *testing.T, server Server) client.StatusClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- server.Run(ctx) }()
	statusClient := client.StatusClient{SocketPath: api.SocketPath(server.RuntimeDir), Timeout: 500 * time.Millisecond}
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer readyCancel()
	if _, err := statusClient.Status(readyCtx); err != nil {
		cancel()
		<-errCh
		t.Fatalf("start test daemon: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("stop test daemon: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("test daemon did not stop")
		}
	})
	return statusClient
}
