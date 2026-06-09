package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/profile"
)

const (
	DefaultTunSOCKSListen = "127.0.0.1"
	DefaultTunSOCKSPort   = uint16(1081)
)

// XrayTunConfigOptions selects the private local SOCKS endpoint used by the
// TUN adapter. Unlike proxy-only listeners, this endpoint is daemon-internal
// runtime plumbing and must not be advertised as a user proxy service.
type XrayTunConfigOptions struct {
	SOCKSListen string
	SOCKSPort   uint16
}

// DefaultXrayTunConfigOptions returns the daemon-private local endpoint used by
// the supported TUN adapter design: tunwarden0 -> tun2socks -> Xray SOCKS ->
// configured Xray outbound.
func DefaultXrayTunConfigOptions() XrayTunConfigOptions {
	return XrayTunConfigOptions{SOCKSListen: DefaultTunSOCKSListen, SOCKSPort: DefaultTunSOCKSPort}
}

// GenerateXrayTunConfig builds deterministic Xray JSON for TUN mode.
//
// Xray-core is still the protocol engine. The Linux TUN device itself is owned
// by TunWarden's network transaction, and packet attachment is performed by the
// daemon-supervised TUN adapter. Therefore this config exposes only a private
// SOCKS inbound for the adapter and must not be reused as a user-visible
// proxy-only config.
func GenerateXrayTunConfig(p profile.Profile, opts XrayTunConfigOptions) ([]byte, error) {
	if opts.SOCKSListen == "" {
		return nil, fmt.Errorf("TUN-mode Xray config requires a SOCKS listen address")
	}
	if opts.SOCKSPort == 0 {
		return nil, fmt.Errorf("TUN-mode Xray config requires a SOCKS listen port")
	}
	if p.Engine != profile.EngineXray {
		return nil, fmt.Errorf("TUN-mode Xray config requires engine %q, got %q", profile.EngineXray, p.Engine)
	}
	if strings.ToLower(p.Protocol) != "vless" {
		return nil, fmt.Errorf("TUN-mode Xray config supports VLESS profiles only, got %q", p.Protocol)
	}
	if strings.TrimSpace(p.UserIdentity) == "" {
		return nil, fmt.Errorf("TUN-mode Xray config requires VLESS user_identity")
	}
	if encryption := strings.ToLower(vlessEncryption(p)); encryption != "none" {
		return nil, fmt.Errorf("unsupported TUN-mode VLESS encryption %q", p.Encryption)
	}

	streamSettings, err := vlessStreamSettings(p)
	if err != nil {
		return nil, err
	}

	cfg := xrayConfig{
		Log: xrayLog{LogLevel: "warning"},
		Inbounds: []xrayInbound{{
			Tag:      "tunwarden-tun-socks",
			Listen:   opts.SOCKSListen,
			Port:     opts.SOCKSPort,
			Protocol: "socks",
			Settings: xraySOCKSInboundSettings{Auth: "noauth", UDP: true, UserLevel: 0},
		}},
		Outbounds: []xrayOutbound{{
			Tag:      "tunwarden-tun-proxy",
			Protocol: "vless",
			Settings: xrayVLESSSettings{
				Address:    p.Server,
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
