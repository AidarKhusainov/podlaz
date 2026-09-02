package api

import "testing"

func issue262CoreExitedStatus(health TunHealthStatus) StatusResponse {
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

func TestIssue262StatusAllowsBoundedTunReconciliationAfterCoreExit(t *testing.T) {
	status := issue262CoreExitedStatus(TunHealthStatus{
		State:             TunHealthRevalidating,
		NetworkGeneration: 4,
		Classification:    TunHealthOwnedStateReconciling,
	})
	if err := ValidateStatusResponse(status); err != nil {
		t.Fatalf("degraded-core reconciliation health must remain publishable: %v", err)
	}
}

func TestIssue262StatusRejectsVerifiedHealthAfterCoreExit(t *testing.T) {
	status := issue262CoreExitedStatus(TunHealthStatus{
		State:             TunHealthVerified,
		NetworkGeneration: 4,
	})
	if err := ValidateStatusResponse(status); err == nil {
		t.Fatal("core-exited TUN unexpectedly accepted verified health")
	}
}
