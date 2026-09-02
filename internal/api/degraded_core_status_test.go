package api

import "testing"

func coreExitedStatus(health TunHealthStatus) StatusResponse {
	return StatusResponse{
		Daemon:           "running",
		Service:          ServiceManual,
		Connection:       ConnectionCoreExited,
		Mode:             "tun",
		RuntimeDirectory: "/run/podlaz",
		Proxy:            "inactive",
		TUN:              "degraded",
		TunHealth:        &health,
	}
}

func TestStatusAllowsBoundedTunReconciliationAfterCoreExit(t *testing.T) {
	status := coreExitedStatus(TunHealthStatus{
		State:             TunHealthRevalidating,
		NetworkGeneration: 4,
		Classification:    TunHealthOwnedStateReconciling,
	})
	if err := ValidateStatusResponse(status); err != nil {
		t.Fatalf("degraded-core reconciliation health must remain publishable: %v", err)
	}
}

func TestStatusRejectsVerifiedHealthAfterCoreExit(t *testing.T) {
	status := coreExitedStatus(TunHealthStatus{
		State:             TunHealthVerified,
		NetworkGeneration: 4,
	})
	if err := ValidateStatusResponse(status); err == nil {
		t.Fatal("core-exited TUN unexpectedly accepted verified health")
	}
}
