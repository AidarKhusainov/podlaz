package daemon

import (
	"context"
	"os"
	"os/exec"
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
	manager := &XrayManager{RuntimeDir: runtimeDir}
	manager.state = xrayState{
		Connection:    "error (core exited)",
		Mode:          planner.ModeTun,
		TransactionID: "tun-crashed",
	}
	statusClient := startCoreExitTestServer(t, Server{
		RuntimeDir: runtimeDir,
		Lifecycle:  manager,
		Status: func(context.Context) api.StatusResponse {
			return api.StatusResponse{
				Daemon:           "running",
				Service:          api.ServiceManual,
				Connection:       "error (core exited)",
				Mode:             planner.ModeTun,
				RuntimeDirectory: "present",
				Proxy:            "inactive",
				TUN:              "error",
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
}

func TestUnexpectedTunCoreExitRefreshesRecoverySnapshotWithBoundedContext(t *testing.T) {
	t.Setenv(api.ServiceEnv, api.ServiceManual)
	runtimeDir := t.TempDir()
	manager := &XrayManager{RuntimeDir: runtimeDir}
	var scanCalls atomic.Int32
	var boundedRefresh atomic.Bool
	statusClient := startCoreExitTestServer(t, Server{
		RuntimeDir: runtimeDir,
		Lifecycle:  manager,
		startupScan: func(ctx context.Context) recovery.PlanResult {
			call := scanCalls.Add(1)
			if call == 1 {
				return recovery.PlanResult{Candidates: []recovery.Candidate{{Kind: "tun-interface", Target: "podlaz0", Description: "stale TUN interface"}}}
			}
			if deadline, ok := ctx.Deadline(); ok {
				remaining := time.Until(deadline)
				boundedRefresh.Store(remaining > 0 && remaining <= 6*time.Second)
			}
			return recovery.PlanResult{}
		},
	})

	configPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeXray := writeFakeXray(t, "#!/bin/sh\nsleep 0.05\nexit 23\n")
	cmd := exec.Command(fakeXray)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	manager.mu.Lock()
	manager.cmd = cmd
	manager.done = done
	manager.state = xrayState{
		Connection:        "active",
		Mode:              planner.ModeTun,
		ProfileID:         "profile-test",
		Proxy:             "active",
		TUN:               "enabled (podlaz0)",
		RuntimeConfigPath: configPath,
		TransactionID:     "tun-crashed",
	}
	manager.mu.Unlock()
	go manager.waitForExit(cmd, done, nil, configPath, "profile-test")

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("fake core did not exit")
	}
	deadline := time.Now().Add(2 * time.Second)
	for scanCalls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	status, err := statusClient.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if scanCalls.Load() < 2 {
		t.Fatalf("unexpected core exit did not refresh recovery state; calls=%d status=%#v", scanCalls.Load(), status.StartupScan)
	}
	if !boundedRefresh.Load() {
		t.Fatal("unexpected-exit recovery refresh did not receive a bounded context")
	}
	if status.StartupScan == nil || status.StartupScan.Status != api.StartupScanStatusClean || len(status.StartupScan.Candidates) != 0 {
		t.Fatalf("status published stale pre-exit recovery evidence: %#v", status.StartupScan)
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
