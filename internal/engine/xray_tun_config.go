package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const (
	DefaultXrayTunName    = "podlaz0"
	DefaultXrayTunMTU     = 1500
	DefaultXrayTunGateway = "198.51.100.1/30"
	DefaultXrayTunDNS     = "1.1.1.1"
)

type XrayTunConfigOptions struct {
	Name                    string
	MTU                     int
	Gateway                 []string
	DNS                     []string
	OutboundAddressOverride string
}

func DefaultXrayTunConfigOptions() XrayTunConfigOptions {
	return XrayTunConfigOptions{
		Name:    DefaultXrayTunName,
		MTU:     DefaultXrayTunMTU,
		Gateway: []string{DefaultXrayTunGateway},
		DNS:     []string{DefaultXrayTunDNS},
	}
}

type xrayTunConfig struct {
	Log       xrayLog          `json:"log"`
	Inbounds  []xrayTunInbound `json:"inbounds"`
	Outbounds []xrayOutbound   `json:"outbounds"`
}

type xrayTunInbound struct {
	Tag      string                 `json:"tag"`
	Protocol string                 `json:"protocol"`
	Settings xrayTunInboundSettings `json:"settings"`
}

type xrayTunInboundSettings struct {
	Name      string   `json:"name"`
	MTU       int      `json:"mtu"`
	Gateway   []string `json:"gateway"`
	DNS       []string `json:"dns"`
	UserLevel int      `json:"userLevel"`
}

// GenerateXrayTunConfig builds deterministic Xray JSON for TUN mode.
//
// Xray-core owns packet ingestion through the native tun inbound and creates or
// attaches the configured TUN link. podlazd remains responsible for the
// transaction-backed Linux host state around that link: route bypass, policy
// rules, DNS, nftables, rollback, and recovery.
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
	if len(opts.Gateway) == 0 {
		return nil, errors.New("TUN-mode Xray config requires at least one TUN gateway")
	}
	if len(opts.DNS) == 0 {
		return nil, errors.New("TUN-mode Xray config requires at least one DNS server")
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
				Gateway:   append([]string(nil), opts.Gateway...),
				DNS:       append([]string(nil), opts.DNS...),
				UserLevel: 0,
			},
		}},
		Outbounds: []xrayOutbound{{
			Tag:      "podlaz-tun-proxy",
			Protocol: "vless",
			Settings: xrayVLESSSettings{
				Address:    outboundAddress,
				Port:       p.Port,
				ID:         p.UserIdentity,
				Encryption: vlessEncryption(p),
				Flow:       strings.TrimSpace(p.Flow),
				Level:      0,
			},
			StreamSettings: streamSettings,
		}},
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
	if len(opts.Gateway) == 0 {
		opts.Gateway = []string{DefaultXrayTunGateway}
	} else {
		opts.Gateway = compactNonEmptyStrings(opts.Gateway)
	}
	if len(opts.DNS) == 0 {
		opts.DNS = []string{DefaultXrayTunDNS}
	} else {
		opts.DNS = compactNonEmptyStrings(opts.DNS)
	}
	opts.OutboundAddressOverride = strings.TrimSpace(opts.OutboundAddressOverride)
	return opts
}

func compactNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
