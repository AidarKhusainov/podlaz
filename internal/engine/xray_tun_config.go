package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const (
	DefaultXrayTunName = "podlaz0"
	DefaultXrayTunMTU  = 1500
)

type XrayTunConfigOptions struct {
	Name                    string
	MTU                     int
	OutboundAddressOverride string
}

func DefaultXrayTunConfigOptions() XrayTunConfigOptions {
	return XrayTunConfigOptions{
		Name: DefaultXrayTunName,
		MTU:  DefaultXrayTunMTU,
	}
}

type xrayTunConfig struct {
	Log       xrayLog           `json:"log"`
	Inbounds  []xrayTunInbound  `json:"inbounds"`
	Outbounds []map[string]any  `json:"outbounds"`
}

type xrayTunInbound struct {
	Tag      string                 `json:"tag"`
	Protocol string                 `json:"protocol"`
	Settings xrayTunInboundSettings `json:"settings"`
}

type xrayTunInboundSettings struct {
	Name      string `json:"name"`
	MTU       int    `json:"MTU"`
	UserLevel int    `json:"userLevel"`
}

// GenerateXrayTunConfig builds deterministic Xray JSON for TUN mode.
//
// The packaged Xray version owns packet ingestion through its native tun
// inbound. podlazd remains responsible for transaction-backed Linux host state
// around that link: route bypass, policy rules, DNS, nftables, rollback, and
// recovery.
func GenerateXrayTunConfig(p profile.Profile, opts XrayTunConfigOptions) ([]byte, error) {
	if profile.IsProviderXrayConfigProfile(p) {
		return nil, unsupportedProviderXrayTunModeError()
	}
	opts = normalizeXrayTunOptions(opts)
	if opts.Name == "" {
		return nil, errors.New("TUN-mode Xray config requires a TUN interface name")
	}
	if opts.MTU <= 0 {
		return nil, errors.New("TUN-mode Xray config requires a positive MTU")
	}
	if err := ValidateXrayTunProfile(p); err != nil {
		return nil, err
	}

	streamSettings, err := vlessStreamSettings("TUN-mode", p)
	if err != nil {
		return nil, err
	}
	outboundAddress := strings.TrimSpace(opts.OutboundAddressOverride)
	if outboundAddress == "" {
		outboundAddress = p.Server
	}

	cfg := xrayTunConfig{
		Log: xrayLog{LogLevel: "warning"},
		Inbounds: []xrayTunInbound{{
			Tag:      "podlaz-tun",
			Protocol: "tun",
			Settings: xrayTunInboundSettings{
				Name:      opts.Name,
				MTU:       opts.MTU,
				UserLevel: 0,
			},
		}},
		Outbounds: []map[string]any{xrayTunOutboundConfig(p, outboundAddress, streamSettings)},
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode TUN-mode Xray config: %w", err)
	}
	return append(out, '\n'), nil
}

func normalizeXrayTunOptions(opts XrayTunConfigOptions) XrayTunConfigOptions {
	if strings.TrimSpace(opts.Name) == "" {
		opts.Name = DefaultXrayTunName
	} else {
		opts.Name = strings.TrimSpace(opts.Name)
	}
	if opts.MTU == 0 {
		opts.MTU = DefaultXrayTunMTU
	}
	opts.OutboundAddressOverride = strings.TrimSpace(opts.OutboundAddressOverride)
	return opts
}
