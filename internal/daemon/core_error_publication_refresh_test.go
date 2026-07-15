package daemon

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func TestStatusPublicationRefreshesStableTunCoreErrorSnapshot(t *testing.T) {
	t.Setenv(api.ServiceEnv, api.ServiceManual)
	runtimeDir := t.TempDir()
	manager := &XrayManager{RuntimeDir: runtimeDir}
	manager.state = xrayState{
		Connection:        "error (core exited)",
		Mode:              planner.ModeTun,
		ProfileID:         "profile-test",
		RuntimeConfigPath: filepath.Join(runtimeDir, generatedDirName, generatedXrayName),
		TransactionID:     "tun-crashed",
		Proxy:             "inactive",
		TUN:               "error",
	}
	var calls atomic.Int32
	var bounded atomic.Bool
	statusClient := startCoreExitTestServer(t, Server{
		RuntimeDir: runtimeDir,
		Lifecycle:  manager,
		startupScan: func(ctx context.Context) recovery.PlanResult {
			if calls.Add(1) == 1 {
				return recovery.PlanResult{Candidates: []recovery.Candidate{{Kind: "tun-interface", Target: "podlaz0", Description: "pre-exit TUN interface"}}}
			}
			if deadline, ok := ctx.Deadline(); ok {
				remaining := time.Until(deadline)
				bounded.Store(remaining > 0 && remaining <= unexpectedCoreExitRefreshTimeout)
			}
			return recovery.PlanResult{}
		},
	})

	status, err := statusClient.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 3 {
		t.Fatalf("status publication did not recollect core-error recovery state; calls=%d", calls.Load())
	}
	if !bounded.Load() {
		t.Fatal("core-error publication refresh did not use the bounded context")
	}
	if status.StartupScan == nil || status.StartupScan.Status != api.StartupScanStatusClean || len(status.StartupScan.Candidates) != 0 {
		t.Fatalf("status published pre-exit stale evidence: %#v", status.StartupScan)
	}
	if status.ActiveTransactionID != "" {
		t.Fatalf("core error status advertised an active transaction: %#v", status)
	}
}
