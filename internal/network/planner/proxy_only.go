package planner

import (
	"fmt"

	"github.com/AidarKhusainov/tunwarden/internal/engine"
	"github.com/AidarKhusainov/tunwarden/internal/profile"
)

const (
	ModeProxyOnly            = "proxy-only"
	DefaultRuntimeConfigPath = "/run/tunwarden/generated/xray.json"
	NoSystemNetworkingPlan   = "Will not modify TUN, routes, DNS, nftables, or firewall."
)

// Listener describes one local proxy listener that would be created by Xray.
type Listener struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     uint16 `json:"port"`
}

// Endpoint returns the printable host:port endpoint for the listener.
func (l Listener) Endpoint() string {
	return fmt.Sprintf("%s:%d", l.Address, l.Port)
}

// ProxyOnlyPlan is the inspectable dry-run plan for proxy-only mode.
type ProxyOnlyPlan struct {
	Mode              string
	ProfileID         string
	ProfileName       string
	RuntimeConfigPath string
	Listeners         []Listener
	Warnings          []string
	Steps             []string
	RollbackSteps     []string
	XrayConfig        []byte
}

// PlanProxyOnly builds a pure dry-run plan for a stored profile without writing files or starting Xray.
func PlanProxyOnly(p profile.Profile) (ProxyOnlyPlan, error) {
	opts := engine.DefaultXrayProxyOnlyConfigOptions()
	xrayConfig, err := engine.GenerateXrayProxyOnlyConfig(p, opts)
	if err != nil {
		return ProxyOnlyPlan{}, err
	}

	listeners := []Listener{
		{Protocol: "SOCKS", Address: opts.SOCKSListen, Port: opts.SOCKSPort},
		{Protocol: "HTTP", Address: opts.HTTPListen, Port: opts.HTTPPort},
	}

	return ProxyOnlyPlan{
		Mode:              ModeProxyOnly,
		ProfileID:         p.ID,
		ProfileName:       p.Name,
		RuntimeConfigPath: DefaultRuntimeConfigPath,
		Listeners:         listeners,
		Steps: []string{
			"Generate runtime Xray config in memory for " + DefaultRuntimeConfigPath,
			"Listen on SOCKS " + listeners[0].Endpoint(),
			"Listen on HTTP " + listeners[1].Endpoint(),
			"Leave TUN, routes, DNS, nftables, and firewall unchanged",
		},
		RollbackSteps: []string{},
		XrayConfig:    xrayConfig,
	}, nil
}
