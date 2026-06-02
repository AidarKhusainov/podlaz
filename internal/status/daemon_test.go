package status

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/api"
)

func TestFromDaemonRendersDaemonBackedStatus(t *testing.T) {
	report := FromDaemon(api.StatusResponse{
		Daemon:           "running",
		Connection:       "inactive",
		RuntimeDirectory: "present",
		Proxy:            "inactive",
		TUN:              "disabled",
	})

	if report.HasUnhealthyState() {
		t.Fatalf("daemon-backed inactive status should be healthy: %#v", report)
	}
	got := report.String()
	for _, want := range []string{"Daemon: running", "Connection: inactive", "Runtime directory: present", "Proxy: inactive", "TUN: disabled", "Stale state: none"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got %q", want, got)
		}
	}
}

func TestWithDaemonUnavailableKeepsCleanFallbackHealthy(t *testing.T) {
	report := WithDaemonUnavailable(Report{
		Daemon:           "not running",
		Connection:       "inactive",
		RuntimeDirectory: RuntimeDirectory{Message: "missing"},
		Proxy:            "inactive",
		TUN:              "not managed in this build",
	}, "daemon socket missing; start tunwardend")

	if report.HasUnhealthyState() {
		t.Fatalf("daemon unavailable with clean local fallback should not be unhealthy: %#v", report)
	}
	if got := report.String(); !strings.Contains(got, "not reachable") || !strings.Contains(got, "using local fallback") {
		t.Fatalf("expected clear unavailable fallback output, got %q", got)
	}
}
