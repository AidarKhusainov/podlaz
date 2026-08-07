package status

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestFromDaemonRendersActiveRuntimeWarningWithoutInspectionFailure(t *testing.T) {
	report := FromDaemon(api.StatusResponse{
		Daemon:           "running",
		Service:          api.ServiceSystemd,
		Connection:       "active",
		Mode:             "tun",
		RuntimeDirectory: "present",
		Proxy:            "active",
		TUN:              "active",
		Routes:           "configured",
		DNS:              "configured",
		Firewall:         "configured",
		Warnings:         []string{"physical interface MTU is lower than the configured TUN MTU"},
		StartupScan: &api.StartupScanStatus{
			Status: api.StartupScanStatusClean,
		},
	})

	if report.HasUnhealthyState() {
		t.Fatalf("an active lifecycle warning without recovery or inspection failures must not be classified as unhealthy: %#v", report)
	}
	got := report.String()
	if strings.Contains(got, "Inspection warnings:") || strings.Contains(got, "could not inspect daemon") {
		t.Fatalf("runtime warning must not be rendered as an inspection failure: %q", got)
	}
	for _, want := range []string{
		"Runtime warnings:\n",
		"  - physical interface MTU is lower than the configured TUN MTU\n",
		"Startup recovery scan: clean for active connection\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected active status output to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "clean inactive state") {
		t.Fatalf("active status must not describe its recovery scan as inactive: %q", got)
	}
}
