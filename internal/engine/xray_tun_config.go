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
	EgressMark              uint32
}

func DefaultXrayTunConfigOptions() XrayTunConfigOptions {
	return XrayTunConfigOptions{
		Name: DefaultXrayTunName,
		MTU:  DefaultXrayTunMTU,
	}
}

type xrayTunConfig struct {
	Log       xrayLog          `json:"log"`
	Inbounds  []xrayTunInbound `json:"inbounds"`
	Outbounds []map[string]any `json:"outbounds"`
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
	opts = normalizeXrayTunOptions(opts)
	if profile.IsProviderXrayConfigProfile(p) {
		return GenerateProviderXrayTunConfig(p, opts)
	}
	if opts.Name == "" {
		return nil, errors.New("TUN-mode Xray config requires a TUN interface name")
	}
	if opts.MTU <= 0 {
		return nil, errors.New("TUN-mode Xray config requires a positive MTU")
	}
	if err := ValidateXrayTunProfile(p); err != nil {
		return nil, err
	}

	outbound, err := typedXrayOutboundConfig(p, p.Server, "podlaz-tun-proxy", "TUN-mode")
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
		Outbounds: []map[string]any{typedXrayTunOutbound(p, outboundAddress, outbound, opts.EgressMark)},
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

func typedXrayTunOutbound(p profile.Profile, address string, outbound xrayOutbound, mark uint32) map[string]any {
	if strings.EqualFold(p.Protocol, "vless") {
		return xrayTunOutboundConfig(p, address, outbound.StreamSettings)
	}
	stream := make(map[string]any, len(outbound.StreamSettings)+1)
	for key, value := range outbound.StreamSettings {
		stream[key] = value
	}
	if mark != 0 {
		stream["sockopt"] = map[string]any{"mark": mark}
	}
	settings := outbound.Settings.(map[string]any)
	fields := make(map[string]any, len(settings))
	for key, value := range settings {
		fields[key] = value
	}
	fields["address"] = address
	return map[string]any{
		"tag": outbound.Tag, "protocol": outbound.Protocol,
		"settings": fields, "streamSettings": stream,
	}
}
