package engine

import (
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

// typedXrayOutboundConfig covers only supported imported protocol fields.
// Arbitrary provider JSON stays with the schema-opaque native boundary.
func typedXrayOutboundConfig(p profile.Profile, address, tag, mode string) (xrayOutbound, error) {
	if p.Engine != profile.EngineXray || strings.TrimSpace(p.UserIdentity) == "" || strings.TrimSpace(address) == "" || p.Port == 0 {
		return xrayOutbound{}, fmt.Errorf("invalid %s typed Xray connection profile", mode)
	}
	protocol := strings.ToLower(p.Protocol)
	if protocol == "vless" {
		if err := validateXrayVLESSProfile(p, mode); err != nil {
			return xrayOutbound{}, err
		}
		stream, err := vlessStreamSettings(mode, p)
		if err != nil {
			return xrayOutbound{}, err
		}
		return xrayOutbound{
			Tag: tag, Protocol: "vless",
			Settings: xrayVLESSSettings{
				Address: address, Port: p.Port, ID: p.UserIdentity,
				Encryption: vlessEncryption(p), Flow: strings.TrimSpace(p.Flow), Level: 0,
			},
			StreamSettings: stream,
		}, nil
	}
	if p.Flow != "" || p.RealityPublicKey != "" || p.RealityShortID != "" || p.RealitySpiderX != "" {
		return xrayOutbound{}, fmt.Errorf("unsupported %s %s security settings", mode, protocol)
	}
	switch strings.ToLower(strings.TrimSpace(p.Transport)) {
	case "", "tcp", "raw", "ws", "websocket", "grpc":
	default:
		return xrayOutbound{}, fmt.Errorf("unsupported %s %s transport", mode, protocol)
	}
	security := strings.ToLower(strings.TrimSpace(p.Security))
	if protocol == "trojan" && security == "" {
		security = "tls"
	}
	if security != "" && security != "tls" && security != "none" {
		return xrayOutbound{}, fmt.Errorf("unsupported %s %s security", mode, protocol)
	}
	stream, err := vlessStreamSettings(mode, p)
	if err != nil {
		return xrayOutbound{}, err
	}
	outbound := xrayOutbound{Tag: tag, Protocol: protocol, StreamSettings: stream}
	switch protocol {
	case "vmess":
		switch p.Encryption {
		case "", "auto", "aes-128-gcm", "chacha20-poly1305":
		default:
			return xrayOutbound{}, fmt.Errorf("unsupported %s VMess encryption", mode)
		}
		cipher := p.Encryption
		if cipher == "" {
			cipher = "auto"
		}
		outbound.Settings = map[string]any{
			"address": address, "port": p.Port, "id": p.UserIdentity,
			"security": cipher, "level": 0,
		}
	case "trojan":
		if p.Encryption != "" {
			return xrayOutbound{}, fmt.Errorf("unsupported %s Trojan encryption", mode)
		}
		if security != "tls" {
			return xrayOutbound{}, fmt.Errorf("unsupported %s Trojan security without TLS", mode)
		}
		outbound.Settings = map[string]any{
			"address": address, "port": p.Port, "password": p.UserIdentity, "level": 0,
		}
	case "shadowsocks":
		method, password, ok := strings.Cut(p.UserIdentity, ":")
		if !ok || method == "" || password == "" || !strings.EqualFold(method, p.Encryption) {
			return xrayOutbound{}, fmt.Errorf("invalid %s Shadowsocks credentials", mode)
		}
		if security != "" && security != "none" {
			return xrayOutbound{}, fmt.Errorf("unsupported %s Shadowsocks TLS", mode)
		}
		if p.Transport != "" || p.ServerName != "" || p.ALPN != "" || p.Fingerprint != "" || p.Path != "" || p.HostHeader != "" || p.ServiceName != "" {
			return xrayOutbound{}, fmt.Errorf("unsupported %s Shadowsocks transport settings", mode)
		}
		outbound.Settings = map[string]any{
			"address": address, "port": p.Port, "method": method,
			"password": password, "level": 0,
		}
		outbound.StreamSettings = map[string]any{"network": "raw", "security": "none"}
	default:
		return xrayOutbound{}, fmt.Errorf("unsupported %s Xray protocol", mode)
	}
	return outbound, nil
}
