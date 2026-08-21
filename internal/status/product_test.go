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
