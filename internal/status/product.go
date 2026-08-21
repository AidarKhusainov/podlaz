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
)

type ProductStatusView struct {
	State          ProductState
	ProfileName    string
	Mode           string
	Reason         string
	AutostartKnown bool
	Autostart      bool
}

func (r Report) ProductView(autostart *api.AutostartStatusResponse) ProductStatusView {
	view := ProductStatusView{ProfileName: r.ProfileName, Mode: r.Mode}
	switch {
	case r.Connection == "connecting":
		view.State = ProductConnecting
	case r.ProductReconnecting:
		view.State = ProductReconnecting
	case r.Connection == "active":
		view.State = ProductConnected
	default:
		view.State = ProductDisconnected
		if r.Health() == LifecycleHealthUnhealthy || r.HasUnhealthyState() {
			view.Reason = "Connection state requires diagnostic attention"
		}
	}
	if autostart != nil {
		view.AutostartKnown = true
		view.Autostart = autostart.Enabled
	}
	return view
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
