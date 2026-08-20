package api

import "testing"

func TestIssue262StatusAllowsBoundedTunReconciliationAfterCoreExit(t *testing.T) {
	status := StatusResponse{
		Connection: "error (core exited)",
		Mode:       "tun",
		TunHealth: &TunHealthStatus{
			State:             TunHealthRevalidating,
			NetworkGeneration: 4,
			Classification:    TunHealthOwnedStateReconciling,
		},
	}
	if err := ValidateStatusResponse(status); err != nil {
		t.Fatalf("degraded-core reconciliation health must remain publishable: %v", err)
	}
}

func TestIssue262StatusRejectsVerifiedHealthAfterCoreExit(t *testing.T) {
	status := StatusResponse{
		Connection: "error (core exited)",
		Mode:       "tun",
		TunHealth: &TunHealthStatus{
			State:             TunHealthVerified,
			NetworkGeneration: 4,
		},
	}
	if err := ValidateStatusResponse(status); err == nil {
		t.Fatal("core-exited TUN unexpectedly accepted verified health")
	}
}
