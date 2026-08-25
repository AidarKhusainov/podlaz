package status

import (
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

type ProductState string

const (
	ProductConnected    ProductState = "Connected"
	ProductConnecting   ProductState = "Connecting"
	ProductReconnecting ProductState = "Reconnecting"
	ProductDisconnected ProductState = "Disconnected"
	ProductUnknown      ProductState = "Unknown"
)

type ProductStatusView struct {
	State          ProductState
	ProfileName    string
	Mode           string
	Reason         string
	AutostartKnown bool
	Autostart      bool
}

func (r Report) ProductView(autostart *api.AutostartStatusResponse, terminalReasons ...api.TerminalReason) ProductStatusView {
	view := ProductStatusView{ProfileName: r.ProfileName, Mode: r.Mode}
	switch {
	case r.Connection == "connecting":
		view.State = ProductConnecting
	case r.ProductReconnecting:
		view.State = ProductReconnecting
	case r.Connection == "active":
		view.State = ProductConnected
	case r.Connection == "inactive":
		view.State = ProductDisconnected
	default:
		view.State = ProductUnknown
		if r.Health() == LifecycleHealthUnhealthy || r.HasUnhealthyState() {
			view.Reason = "Connection state could not be determined"
		}
	}
	if len(terminalReasons) > 0 && view.State == ProductDisconnected {
		view.Reason = productTerminalReasonMessage(terminalReasons[0])
	}
	if autostart != nil {
		view.AutostartKnown = true
		view.Autostart = autostart.Enabled
	}
	return view
}

func productTerminalReasonMessage(reason api.TerminalReason) string {
	switch reason {
	case api.TerminalReasonVPNConnectFailed:
		return "VPN connection could not be established safely"
	case api.TerminalReasonVPNRestoreFailed:
		return "VPN connection could not be restored safely"
	case api.TerminalReasonBootNetworkNotReady:
		return "Network was not ready for VPN autostart"
	default:
		return ""
	}
}

func (v ProductStatusView) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Status: %s\n", v.State)
	if v.ProfileName != "" {
		fmt.Fprintf(&b, "Profile: %s\n", render.Redact(v.ProfileName))
	}
	if v.Mode != "" {
		fmt.Fprintf(&b, "Mode: %s\n", render.Redact(v.Mode))
	}
	if v.Reason != "" {
		fmt.Fprintf(&b, "Reason: %s\n", render.Redact(v.Reason))
	}
	if v.AutostartKnown {
		if v.Autostart {
			b.WriteString("Autostart: Enabled for next boot\n")
		} else {
			b.WriteString("Autostart: Disabled\n")
		}
	}
	return b.String()
}
