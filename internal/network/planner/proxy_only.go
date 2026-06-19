package planner

import (
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const (
	ModeProxyOnly            = "proxy-only"
	DefaultRuntimeConfigPath = "/run/podlaz/generated/xray.json"
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

// ProxyOnlyOptions controls generated runtime output details for proxy-only mode.
type ProxyOnlyOptions struct {
	RuntimeConfigPath string
	Config            engine.XrayProxyOnlyConfigOptions
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
	return PlanProxyOnlyWithOptions(p, ProxyOnlyOptions{})
}

// PlanProxyOnlyWithOptions builds a proxy-only plan for a stored profile using injectable runtime details.
func PlanProxyOnlyWithOptions(p profile.Profile, opts ProxyOnlyOptions) (ProxyOnlyPlan, error) {
	configOpts := opts.Config
	if configOpts == (engine.XrayProxyOnlyConfigOptions{}) {
		configOpts = engine.DefaultXrayProxyOnlyConfigOptions()
	}
	runtimeConfigPath := opts.RuntimeConfigPath
	if runtimeConfigPath == "" {
		runtimeConfigPath = DefaultRuntimeConfigPath
	}

	xrayConfig, err := engine.GenerateXrayProxyOnlyConfig(p, configOpts)
	if err != nil {
		return ProxyOnlyPlan{}, err
	}

	listeners := []Listener{
		{Protocol: "SOCKS", Address: configOpts.SOCKSListen, Port: configOpts.SOCKSPort},
		{Protocol: "HTTP", Address: configOpts.HTTPListen, Port: configOpts.HTTPPort},
	}

	return ProxyOnlyPlan{
		Mode:              ModeProxyOnly,
		ProfileID:         p.ID,
		ProfileName:       p.Name,
		RuntimeConfigPath: runtimeConfigPath,
		Listeners:         listeners,
		Steps: []string{
			"Generate runtime Xray config in memory for " + runtimeConfigPath,
			"Listen on SOCKS " + listeners[0].Endpoint(),
			"Listen on HTTP " + listeners[1].Endpoint(),
			"Leave TUN, routes, DNS, nftables, and firewall unchanged",
		},
		RollbackSteps: []string{},
		XrayConfig:    xrayConfig,
	}, nil
}
