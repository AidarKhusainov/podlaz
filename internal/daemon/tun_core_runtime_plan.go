package daemon

import (
	"errors"
	"net"
	"regexp"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

var ipv4DiagnosticPattern = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)

func planTunCoreRuntime(p profile.Profile, runtimeConfigPath string, plan planner.TunPlan) (tunCoreRuntimePlan, error) {
	if runtimeConfigPath == "" {
		return tunCoreRuntimePlan{}, errors.New("TUN-mode Xray runtime config requires a runtime config path")
	}
	serverIP, err := requireTunRuntimeServerBypass(plan)
	if err != nil {
		return tunCoreRuntimePlan{}, err
	}

	opts := engine.DefaultXrayTunConfigOptions()
	opts.Name = plan.TunDevice.Name
	opts.MTU = plan.TunDevice.MTU
	opts.OutboundAddressOverride = serverIP
	xrayConfig, err := engine.GenerateXrayTunConfig(p, opts)
	if err != nil {
		return tunCoreRuntimePlan{}, err
	}
	warnings := []string{
		"TUN-mode connectivity is verified before transaction commit",
		"Pinned Xray TUN schema owns packet ingestion only; podlazd owns Linux route and DNS state and fails before commit if route, TCP, or DNS verification does not pass",
		"Xray owns podlaz0 lifecycle; podlazd verifies the link and owns host networking rollback metadata",
	}
	if opts.OutboundAddressOverride != p.Server {
		warnings = append(warnings, "TUN-mode Xray runtime uses the pre-resolved server address")
	}
	return tunCoreRuntimePlan{
		RuntimeConfigPath: runtimeConfigPath,
		XrayConfig:        xrayConfig,
		Status:            "TUN-mode Xray runtime config with native podlaz0 TUN inbound",
		Warnings:          warnings,
	}, nil
}

func requireTunRuntimeServerBypass(plan planner.TunPlan) (string, error) {
	serverIP := tunRuntimeServerAddress(plan)
	if serverIP == "" {
		detail := "TUN mode requires a concrete IPv4 server bypass before generating the native Xray TUN runtime config. Resolve the profile server to an IPv4 address outside podlaz0 before starting TUN mode."
		detail += "\n" + serverBypassDiagnostics(plan)
		return "", newRuntimeUnavailableError("VPN server bypass", detail)
	}
	return serverIP, nil
}

func serverBypassDiagnostics(plan planner.TunPlan) string {
	snapshot := plan.Snapshot
	return strings.Join([]string{
		"Diagnostics:",
		"  server route status: " + safeDiagnosticValue(string(snapshot.ServerRoute.Status)),
		"  server route interface: " + safeDiagnosticValue(snapshot.ServerRoute.Interface),
		"  server route gateway: " + safeDiagnosticValue(snapshot.ServerRoute.Gateway),
		"  server route detail: " + safeDiagnosticValue(snapshot.ServerRoute.Detail),
		"  server route raw: " + safeDiagnosticValue(snapshot.ServerRoute.Raw),
		"  default IPv4 status: " + safeDiagnosticValue(string(snapshot.DefaultIPv4.Status)),
		"  default IPv4 interface: " + safeDiagnosticValue(snapshot.DefaultIPv4.Interface),
		"  default IPv4 gateway: " + safeDiagnosticValue(snapshot.DefaultIPv4.Gateway),
	}, "\n")
}

func safeDiagnosticValue(value string) string {
	value = strings.TrimSpace(render.Redact(value))
	if value == "" {
		return "<missing>"
	}
	return redactPrivateIPv4(value)
}

func redactPrivateIPv4(value string) string {
	return ipv4DiagnosticPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		ip := net.ParseIP(candidate)
		if ip == nil || ip.To4() == nil {
			return candidate
		}
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return "<private-ipv4>"
		}
		return candidate
	})
}

func tunRuntimeServerAddress(plan planner.TunPlan) string {
	serverBypass := strings.TrimSpace(plan.ServerBypass.Destination)
	if serverBypass == "" || serverBypass == "<server-ip>" {
		return ""
	}
	ip, _, err := net.ParseCIDR(serverBypass)
	if err == nil && ip.To4() != nil {
		return ip.String()
	}
	if parsed := net.ParseIP(serverBypass); parsed != nil && parsed.To4() != nil {
		return parsed.String()
	}
	return ""
}
