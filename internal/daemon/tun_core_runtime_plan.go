package daemon

import (
	"errors"
	"net"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

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
		return "", newRuntimeUnavailableError("VPN server bypass", "TUN mode requires a concrete IPv4 server bypass before generating the native Xray TUN runtime config. Resolve the profile server to an IPv4 address outside podlaz0 before starting TUN mode.")
	}
	return serverIP, nil
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
