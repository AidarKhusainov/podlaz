package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const (
	DefaultProxyOnlySOCKSListen = "127.0.0.1"
	DefaultProxyOnlySOCKSPort   = uint16(1080)
	DefaultProxyOnlyHTTPListen  = "127.0.0.1"
	DefaultProxyOnlyHTTPPort    = uint16(8080)
)

// XrayProxyOnlyConfigOptions selects the local proxy listeners for a dry-run Xray config.
type XrayProxyOnlyConfigOptions struct {
	SOCKSListen string
	SOCKSPort   uint16
	HTTPListen  string
	HTTPPort    uint16
}

// DefaultXrayProxyOnlyConfigOptions returns the documented v0.1 local proxy listeners.
func DefaultXrayProxyOnlyConfigOptions() XrayProxyOnlyConfigOptions {
	return XrayProxyOnlyConfigOptions{
		SOCKSListen: DefaultProxyOnlySOCKSListen,
		SOCKSPort:   DefaultProxyOnlySOCKSPort,
		HTTPListen:  DefaultProxyOnlyHTTPListen,
		HTTPPort:    DefaultProxyOnlyHTTPPort,
	}
}

type xrayConfig struct {
	Log       xrayLog        `json:"log"`
	Inbounds  []xrayInbound  `json:"inbounds"`
	Outbounds []xrayOutbound `json:"outbounds"`
}

type xrayLog struct {
	LogLevel string `json:"loglevel"`
}

type xrayInbound struct {
	Tag      string `json:"tag"`
	Listen   string `json:"listen"`
	Port     uint16 `json:"port"`
	Protocol string `json:"protocol"`
	Settings any    `json:"settings"`
}

type xraySOCKSInboundSettings struct {
	Auth      string `json:"auth"`
	UDP       bool   `json:"udp"`
	UserLevel int    `json:"userLevel"`
}

type xrayHTTPInboundSettings struct {
	AllowTransparent bool `json:"allowTransparent"`
	UserLevel        int  `json:"userLevel"`
}

type xrayOutbound struct {
	Tag            string            `json:"tag"`
	Protocol       string            `json:"protocol"`
	Settings       xrayVLESSSettings `json:"settings"`
	StreamSettings map[string]any    `json:"streamSettings"`
}

type xrayVLESSSettings struct {
	Address    string `json:"address"`
	Port       uint16 `json:"port"`
	ID         string `json:"id"`
	Encryption string `json:"encryption"`
	Flow       string `json:"flow,omitempty"`
	Level      int    `json:"level"`
}

// ValidateXrayProxyOnlyProfile checks whether a normalized profile can produce a
// supported proxy-only Xray config without writing runtime state.
func ValidateXrayProxyOnlyProfile(p profile.Profile) error {
	return validateXrayVLESSProfile(p, "proxy-only")
}

// ValidateXrayTunProfile checks whether a normalized profile can produce a
// supported TUN-mode Xray config without writing runtime state.
func ValidateXrayTunProfile(p profile.Profile) error {
	return validateXrayVLESSProfile(p, "TUN-mode")
}

func validateXrayVLESSProfile(p profile.Profile, modeName string) error {
	if p.Engine != profile.EngineXray {
		return fmt.Errorf("%s Xray config requires engine %q, got %q", modeName, profile.EngineXray, p.Engine)
	}
	if strings.ToLower(p.Protocol) != "vless" {
		return fmt.Errorf("%s Xray config supports VLESS profiles only, got %q", modeName, p.Protocol)
	}
	if strings.TrimSpace(p.UserIdentity) == "" {
		return fmt.Errorf("%s Xray config requires VLESS user_identity", modeName)
	}
	if encryption := strings.ToLower(vlessEncryption(p)); encryption != "none" {
		return fmt.Errorf("unsupported %s VLESS encryption %q", modeName, p.Encryption)
	}
	_, err := vlessStreamSettings(modeName, p)
	return err
}

// GenerateXrayProxyOnlyConfig builds deterministic Xray JSON for a proxy-only plan.
func GenerateXrayProxyOnlyConfig(p profile.Profile, opts XrayProxyOnlyConfigOptions) ([]byte, error) {
	if opts.SOCKSListen == "" {
		return nil, fmt.Errorf("proxy-only Xray config requires a SOCKS listen address")
	}
	if opts.SOCKSPort == 0 {
		return nil, fmt.Errorf("proxy-only Xray config requires a SOCKS listen port")
	}
	if opts.HTTPListen == "" {
		return nil, fmt.Errorf("proxy-only Xray config requires an HTTP listen address")
	}
	if opts.HTTPPort == 0 {
		return nil, fmt.Errorf("proxy-only Xray config requires an HTTP listen port")
	}
	if err := ValidateXrayProxyOnlyProfile(p); err != nil {
		return nil, err
	}

	streamSettings, err := vlessStreamSettings("proxy-only", p)
	if err != nil {
		return nil, err
	}

	cfg := xrayConfig{
		Log: xrayLog{LogLevel: "warning"},
		Inbounds: []xrayInbound{
			{
				Tag:      "podlaz-socks",
				Listen:   opts.SOCKSListen,
				Port:     opts.SOCKSPort,
				Protocol: "socks",
				Settings: xraySOCKSInboundSettings{Auth: "noauth", UDP: false, UserLevel: 0},
			},
			{
				Tag:      "podlaz-http",
				Listen:   opts.HTTPListen,
				Port:     opts.HTTPPort,
				Protocol: "http",
				Settings: xrayHTTPInboundSettings{AllowTransparent: false, UserLevel: 0},
			},
		},
		Outbounds: []xrayOutbound{
			{
				Tag:      "podlaz-proxy",
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
			},
		},
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode proxy-only Xray config: %w", err)
	}
	return append(out, '\n'), nil
}

func vlessEncryption(p profile.Profile) string {
	encryption := strings.ToLower(strings.TrimSpace(p.Encryption))
	if encryption == "" {
		return "none"
	}
	return encryption
}

func vlessStreamSettings(modeName string, p profile.Profile) (map[string]any, error) {
	network, err := canonicalVLESSTransport(modeName, p.Transport)
	if err != nil {
		return nil, err
	}
	security, err := canonicalVLESSSecurity(modeName, p.Security)
	if err != nil {
		return nil, err
	}
	if security == "reality" && network != "raw" && network != "grpc" {
		return nil, fmt.Errorf("unsupported %s VLESS transport/security combination: security %q is not compatible with transport %q", modeName, security, network)
	}

	settings := map[string]any{
		"network":  network,
		"security": security,
	}
	if transportSettings := vlessTransportSettings(network, p); len(transportSettings) > 0 {
		settings[transportSettingsKey(network)] = transportSettings
	}

	switch security {
	case "tls":
		if tlsSettings := tlsSettings(p); len(tlsSettings) > 0 {
			settings["tlsSettings"] = tlsSettings
		}
	case "reality":
		realitySettings, err := realitySettings(modeName, p)
		if err != nil {
			return nil, err
		}
		settings["realitySettings"] = realitySettings
	}

	return settings, nil
}

func canonicalVLESSTransport(modeName string, transport string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "", "tcp", "raw":
		return "raw", nil
	case "ws", "websocket":
		return "websocket", nil
	case "grpc":
		return "grpc", nil
	case "httpupgrade":
		return "httpupgrade", nil
	case "xhttp", "quic", "kcp", "mkcp":
		return "", fmt.Errorf("unsupported %s VLESS transport %q for generated Xray config", modeName, transport)
	default:
		return "", fmt.Errorf("unsupported %s VLESS transport %q", modeName, transport)
	}
}

func canonicalVLESSSecurity(modeName string, security string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(security)) {
	case "", "none":
		return "none", nil
	case "tls":
		return "tls", nil
	case "reality":
		return "reality", nil
	default:
		return "", fmt.Errorf("unsupported %s VLESS security %q", modeName, security)
	}
}

func vlessTransportSettings(network string, p profile.Profile) map[string]any {
	settings := map[string]any{}
	switch network {
	case "websocket":
		putIfNotEmpty(settings, "path", p.Path)
		putIfNotEmpty(settings, "host", p.HostHeader)
	case "grpc":
		putIfNotEmpty(settings, "serviceName", p.ServiceName)
		putIfNotEmpty(settings, "authority", p.HostHeader)
	case "httpupgrade":
		putIfNotEmpty(settings, "path", p.Path)
		putIfNotEmpty(settings, "host", p.HostHeader)
	}
	return settings
}

func transportSettingsKey(network string) string {
	switch network {
	case "websocket":
		return "wsSettings"
	case "grpc":
		return "grpcSettings"
	case "httpupgrade":
		return "httpupgradeSettings"
	default:
		return ""
	}
}

func tlsSettings(p profile.Profile) map[string]any {
	settings := map[string]any{}
	putIfNotEmpty(settings, "serverName", p.ServerName)
	putIfNotEmpty(settings, "fingerprint", p.Fingerprint)
	if alpn := splitCommaSeparated(p.ALPN); len(alpn) > 0 {
		settings["alpn"] = alpn
	}
	return settings
}

func realitySettings(modeName string, p profile.Profile) (map[string]any, error) {
	if strings.TrimSpace(p.ServerName) == "" {
		return nil, fmt.Errorf("%s Xray config requires server_name for Reality security", modeName)
	}
	if strings.TrimSpace(p.RealityPublicKey) == "" {
		return nil, fmt.Errorf("%s Xray config requires reality_public_key for Reality security", modeName)
	}
	settings := map[string]any{
		"serverName": strings.TrimSpace(p.ServerName),
		"publicKey":  strings.TrimSpace(p.RealityPublicKey),
	}
	putIfNotEmpty(settings, "fingerprint", p.Fingerprint)
	putIfNotEmpty(settings, "shortId", p.RealityShortID)
	putIfNotEmpty(settings, "spiderX", p.RealitySpiderX)
	return settings, nil
}

func putIfNotEmpty(settings map[string]any, key, value string) {
	if strings.TrimSpace(value) != "" {
		settings[key] = strings.TrimSpace(value)
	}
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
