package status

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestWithTunHealthKeepsVerifiedActiveSessionHealthy(t *testing.T) {
	report := WithTunHealth(Report{Connection: "active", TUN: "enabled (podlaz0)"}, &api.TunHealthStatus{
		State:             api.TunHealthVerified,
		NetworkGeneration: 2,
	})
	if report.Health() != LifecycleHealthHealthy {
		t.Fatalf("verified current health became unhealthy: %#v", report)
	}
	if !strings.Contains(report.TUN, "current health=verified") || !strings.Contains(report.TUN, "network generation=2") {
		t.Fatalf("verified TUN health not rendered: %q", report.TUN)
	}
}

func TestWithTunHealthMakesDegradedActiveSessionUnhealthy(t *testing.T) {
	report := WithTunHealth(Report{Connection: "active", TUN: "enabled (podlaz0)"}, &api.TunHealthStatus{
		State:             api.TunHealthDegraded,
		NetworkGeneration: 3,
		Classification:    api.TunHealthConnectivityFailed,
	})
	if report.Health() != LifecycleHealthUnhealthy {
		t.Fatalf("degraded current health remained healthy: %#v", report)
	}
	if !strings.Contains(report.Connection, "connectivity_failed") || !strings.Contains(report.TUN, "network generation=3") {
		t.Fatalf("degraded TUN health not rendered structurally: connection=%q tun=%q", report.Connection, report.TUN)
	}
}

func TestWithTunHealthRendersNetworkConvergenceWithoutClaimingVerified(t *testing.T) {
	report := WithTunHealth(Report{Connection: "active", TUN: "enabled (podlaz0)"}, &api.TunHealthStatus{
		State:             api.TunHealthRevalidating,
		NetworkGeneration: 4,
		Classification:    api.TunHealthNetworkConverging,
	})
	if report.Health() != LifecycleHealthUnhealthy {
		t.Fatalf("network convergence was rendered healthy: %#v", report)
	}
	if !strings.Contains(report.Connection, "network_converging") || !strings.Contains(report.TUN, "current health=revalidating") {
		t.Fatalf("network convergence not rendered structurally: connection=%q tun=%q", report.Connection, report.TUN)
	}
}

func TestWithTunHealthRendersOwnedStateReconciliation(t *testing.T) {
	report := WithTunHealth(Report{Connection: "active", TUN: "enabled (podlaz0)"}, &api.TunHealthStatus{
		State:             api.TunHealthRevalidating,
		NetworkGeneration: 5,
		Classification:    api.TunHealthOwnedStateReconciling,
	})
	if report.Health() != LifecycleHealthUnhealthy {
		t.Fatalf("owned-state reconciliation was rendered healthy: %#v", report)
	}
	if !strings.Contains(report.Connection, "owned_state_reconciling") || !strings.Contains(report.TUN, "network generation=5") {
		t.Fatalf("owned-state reconciliation not rendered structurally: connection=%q tun=%q", report.Connection, report.TUN)
	}
}
