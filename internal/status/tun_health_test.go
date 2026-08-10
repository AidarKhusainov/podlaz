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
