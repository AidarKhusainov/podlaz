package status

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestProductViewProjectsStableLifecycleStates(t *testing.T) {
	tests := []struct {
		name   string
		report Report
		want   ProductState
	}{
		{name: "connected", report: Report{Connection: "active", ProfileName: "Example VPN", Mode: "tun"}, want: ProductConnected},
		{name: "connecting", report: Report{Connection: "connecting", ProfileName: "Example VPN", Mode: "tun"}, want: ProductConnecting},
		{name: "reconnecting", report: Report{Connection: "active", Mode: "tun", ProductReconnecting: true}, want: ProductReconnecting},
		{name: "disconnected", report: Report{Connection: "inactive"}, want: ProductDisconnected},
		{name: "inspection incomplete", report: Report{Connection: "unknown (inspection incomplete)"}, want: ProductUnknown},
		{name: "stale inactive is not confirmed disconnected", report: Report{Connection: "inactive (stale state detected)"}, want: ProductUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			view := tt.report.ProductView(nil)
			if view.State != tt.want {
				t.Fatalf("state = %q, want %q", view.State, tt.want)
			}
		})
	}
}

func TestProductViewUnknownNeverClaimsDisconnected(t *testing.T) {
	view := (Report{Connection: "unknown (inspection incomplete)"}).ProductView(nil)
	got := view.String()
	if strings.Contains(got, "Status: Disconnected") {
		t.Fatalf("unknown inspection claimed confirmed disconnect: %q", got)
	}
	if !strings.Contains(got, "Status: Unknown") {
		t.Fatalf("unknown inspection missing safe product state: %q", got)
	}
}

func TestProductViewKeepsStableTypedTerminalReasonAfterCleanTeardown(t *testing.T) {
	view := (Report{Connection: "inactive"}).ProductView(nil, api.TerminalReasonVPNRestoreFailed)
	if view.State != ProductDisconnected {
		t.Fatalf("state = %q, want %q", view.State, ProductDisconnected)
	}
	if view.Reason != "VPN connection could not be restored safely" {
		t.Fatalf("reason = %q", view.Reason)
	}
	if got := view.String(); !strings.Contains(got, "Reason: VPN connection could not be restored safely") {
		t.Fatalf("terminal reason not rendered after clean teardown: %q", got)
	}
}

func TestProductViewRendersConciseStatusAndAutostart(t *testing.T) {
	autostart := api.AutostartStatusResponse{Enabled: true, Mode: "tun", ProfileName: "Example VPN"}
	view := (Report{Connection: "active", ProfileName: "Example VPN", Mode: "tun"}).ProductView(&autostart)
	got := view.String()
	for _, want := range []string{"Status: Connected", "Profile: Example VPN", "Mode: tun", "Autostart: Enabled for next boot"} {
		if !strings.Contains(got, want) {
			t.Fatalf("product output missing %q: %q", want, got)
		}
	}
	for _, forbidden := range []string{"Service:", "Runtime config:", "Proxy:", "TUN:", "Routes:", "DNS:", "Firewall:", "Transaction:", "Recovery candidates:"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("product output leaked diagnostic field %q: %q", forbidden, got)
		}
	}
}
