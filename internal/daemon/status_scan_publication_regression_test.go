package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func TestStartupScanForPublicationPreservesCoalescedTimeoutWarningAndRequestDeadline(t *testing.T) {
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	ownerDone := make(chan struct{})
	state := newStartupScanState(func(context.Context) recovery.PlanResult {
		close(firstEntered)
		<-releaseFirst
		return recovery.PlanResult{}
	})
	state.mu.Lock()
	state.scan = recovery.PlanResult{Candidates: []recovery.Candidate{{
		Kind:        "tun-interface",
		Target:      "podlaz0",
		Description: "previous recovery evidence",
	}}}
	state.mu.Unlock()
	go func() {
		defer close(ownerDone)
		state.Refresh(context.Background())
	}()
	<-firstEntered

	manager := &XrayManager{}
	manager.state = xrayState{Connection: "error (core exited)", Mode: planner.ModeTun}
	currentStatus := func(context.Context) api.StatusResponse {
		return publicationRegressionStatus("error (core exited)")
	}
	requestCtx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	status, scan := startupScanForPublication(
		requestCtx,
		currentStatus,
		manager,
		state,
		t.TempDir(),
		200*time.Millisecond,
	)
	elapsed := time.Since(started)
	close(releaseFirst)
	<-ownerDone

	if elapsed > 100*time.Millisecond {
		t.Fatalf("publication refresh ignored request deadline: %s", elapsed)
	}
	if status.Connection != "error (core exited)" {
		t.Fatalf("unexpected status: %#v", status)
	}
	if len(scan.Warnings) == 0 || !strings.Contains(scan.Warnings[len(scan.Warnings)-1].Message, "concurrent recovery scan") {
		t.Fatalf("coalesced refresh warning was not published: %#v", scan.Warnings)
	}
	if got := startupScanStatus(scan); got != api.StartupScanStatusStaleIncomplete {
		t.Fatalf("expected stale incomplete publication, got %q: %#v", got, scan)
	}
}

func TestStartupScanForPublicationRereadsLifecycleStatusAfterRefresh(t *testing.T) {
	manager := &XrayManager{}
	manager.state = xrayState{Connection: "error (core exited)", Mode: planner.ModeTun}
	published := publicationRegressionStatus("error (core exited)")
	state := newStartupScanState(func(context.Context) recovery.PlanResult {
		manager.mu.Lock()
		manager.state = xrayState{
			Connection:        "active",
			Mode:              planner.ModeTun,
			ProfileID:         "profile-test",
			RuntimeConfigPath: "/run/podlaz/generated/xray.json",
			TransactionID:     "tun-reconnected",
		}
		manager.mu.Unlock()
		published = publicationRegressionStatus("active")
		published.ProfileID = "profile-test"
		published.RuntimeConfigPath = "/run/podlaz/generated/xray.json"
		return recovery.PlanResult{}
	})
	currentStatus := func(context.Context) api.StatusResponse { return published }

	status, _ := startupScanForPublication(
		context.Background(),
		currentStatus,
		manager,
		state,
		t.TempDir(),
		time.Second,
	)
	if status.Connection != "active" {
		t.Fatalf("publication returned status captured before refresh: %#v", status)
	}
}

func publicationRegressionStatus(connection string) api.StatusResponse {
	return api.StatusResponse{
		Daemon:           "running",
		Service:          api.ServiceManual,
		Connection:       connection,
		Mode:             planner.ModeTun,
		RuntimeDirectory: "present",
		Proxy:            "inactive",
		TUN:              "error",
	}
}
