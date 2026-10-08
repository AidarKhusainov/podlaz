package sub

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"go.yaml.in/yaml/v3"
)

// mihomoHysteria2 deliberately maps only the interoperable subset into an
// Xray-owned configuration. Do not broaden the flat Podlaz profile schema.
func mihomoHysteria2(entry *yaml.Node, fields map[string]*yaml.Node, source profile.SourceType) (profile.Profile, []string, error) {
	allowed := map[string]bool{
		"name": true, "type": true, "server": true, "port": true,
		"password": true, "sni": true, "alpn": true,
	}
	for i := 0; i < len(entry.Content); i += 2 {
		if !allowed[entry.Content[i].Value] {
			return profile.Profile{}, nil, fmt.Errorf("unsupported Clash/Mihomo Hysteria2 option at line %d", entry.Content[i].Line)
		}
	}
	name, err := mihomoRequiredString(fields, "name", entry.Line)
	if err != nil {
		return profile.Profile{}, nil, err
	}
	server, err := mihomoRequiredString(fields, "server", entry.Line)
	if err != nil {
		return profile.Profile{}, nil, err
	}
	// The native provider profile does not validate its endpoint separately.
	if net.ParseIP(server) == nil && !mihomoHysteriaHostname(server) {
		return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: invalid Hysteria2 server at line %d", entry.Line)
	}
	password, err := mihomoRequiredString(fields, "password", entry.Line)
	if err != nil {
		return profile.Profile{}, nil, err
	}
	portNode, ok := fields["port"]
	if !ok || portNode.Kind != yaml.ScalarNode || portNode.Tag != "!!int" {
		return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: Hysteria2 port must be an integer at line %d", entry.Line)
	}
	port, err := strconv.ParseUint(portNode.Value, 10, 16)
	if err != nil || port == 0 {
		return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: Hysteria2 port must be between 1 and 65535 at line %d", portNode.Line)
	}
	tls := map[string]any{}
	if node, ok := fields["sni"]; ok {
		sni, err := mihomoString(node, "sni")
		if err != nil {
			return profile.Profile{}, nil, err
		}
		if strings.TrimSpace(sni) == "" || strings.ContainsAny(sni, " \t\r\n") {
			return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: invalid Hysteria2 sni at line %d", node.Line)
		}
		tls["serverName"] = sni
	}
	if node, ok := fields["alpn"]; ok {
		if node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
			return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: Hysteria2 alpn must be a nonempty list at line %d", node.Line)
		}
		values := make([]string, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := mihomoString(child, "alpn")
			if err != nil {
				return profile.Profile{}, nil, err
			}
			if value == "" || strings.ContainsAny(value, " \t\r\n") {
				return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: invalid Hysteria2 alpn at line %d", child.Line)
			}
			values = append(values, value)
		}
		tls["alpn"] = values
	}
	raw, err := json.Marshal(map[string]any{
		"outbounds": []any{map[string]any{
			"tag": "proxy", "protocol": "hysteria",
			"settings": map[string]any{"version": 2, "address": server, "port": port},
			"streamSettings": map[string]any{
				"network": "hysteria", "security": "tls",
				"hysteriaSettings": map[string]any{"version": 2, "auth": password},
				"tlsSettings":      tls,
			},
		}},
	})
	if err != nil {
		return profile.Profile{}, nil, fmt.Errorf("encode Clash/Mihomo Hysteria2 configuration")
	}
	p, accepted, err := profile.NewProviderXrayConfig(name, source, raw)
	if err != nil {
		return profile.Profile{}, nil, fmt.Errorf("invalid Clash/Mihomo Hysteria2 configuration at line %d", entry.Line)
	}
	if !accepted {
		return p, []string{profile.DisplayNameRejectedWarning}, nil
	}
	return p, nil, nil
}

func mihomoHysteriaHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}
