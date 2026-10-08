package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"go.yaml.in/yaml/v3"
)

// Translate only Mihomo options represented by existing share-URI importers.
func mihomoExisting(entry *yaml.Node, fields map[string]*yaml.Node, source profile.SourceType, kind string) (profile.Profile, []string, error) {
	allowed := map[string]bool{"name": true, "type": true, "server": true, "port": true}
	switch kind {
	case "vmess":
		for _, key := range []string{"uuid", "alterId", "cipher", "network", "tls", "servername", "sni", "alpn", "client-fingerprint", "ws-opts"} {
			allowed[key] = true
		}
	case "trojan":
		for _, key := range []string{"password", "network", "tls", "sni", "servername", "alpn", "client-fingerprint", "ws-opts", "grpc-opts"} {
			allowed[key] = true
		}
	case "ss":
		for _, key := range []string{"password", "cipher"} {
			allowed[key] = true
		}
	}
	for i := 0; i < len(entry.Content); i += 2 {
		key := entry.Content[i]
		if !allowed[key.Value] {
			return profile.Profile{}, nil, fmt.Errorf("unsupported Clash/Mihomo %s option at line %d", kind, key.Line)
		}
	}
	name, err := mihomoRequiredString(fields, "name", entry.Line)
	if err != nil {
		return profile.Profile{}, nil, err
	}
	host, err := mihomoRequiredString(fields, "server", entry.Line)
	if err != nil {
		return profile.Profile{}, nil, err
	}
	portNode := fields["port"]
	if portNode == nil || portNode.Kind != yaml.ScalarNode || portNode.Tag != "!!int" {
		return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: port must be an integer at line %d", entry.Line)
	}
	port, err := strconv.ParseUint(portNode.Value, 10, 16)
	if err != nil || port == 0 {
		return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: port must be between 1 and 65535 at line %d", portNode.Line)
	}
	endpoint := net.JoinHostPort(host, strconv.FormatUint(port, 10))
	var p profile.Profile
	var warnings []string
	switch kind {
	case "ss":
		method, err := mihomoRequiredString(fields, "cipher", entry.Line)
		if err != nil {
			return p, nil, err
		}
		password, err := mihomoRequiredString(fields, "password", entry.Line)
		if err != nil {
			return p, nil, err
		}
		if strings.TrimSpace(password) != password {
			return p, nil, fmt.Errorf("unsupported Clash/Mihomo ss password whitespace at line %d", entry.Line)
		}
		switch method {
		case "aes-128-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "chacha20-poly1305":
		default:
			return p, nil, fmt.Errorf("unsupported Clash/Mihomo ss cipher at line %d", entry.Line)
		}
		credentials := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + strings.ReplaceAll(password, "%", "%25")))
		link := "ss://" + credentials + "@" + endpoint + "#" + url.PathEscape(name)
		p, warnings, err = profile.ImportShadowsocksURI(link)
		if err != nil || p.UserIdentity != method+":"+password {
			return profile.Profile{}, nil, fmt.Errorf("invalid Clash/Mihomo ss profile at line %d", entry.Line)
		}
	case "vmess", "trojan":
		p, warnings, err = mihomoVMessTrojan(entry, fields, kind, name, host, endpoint, port)
		if err != nil {
			return profile.Profile{}, nil, err
		}
	default:
		return p, nil, fmt.Errorf("unsupported Clash/Mihomo proxy protocol at line %d", entry.Line)
	}
	p.Source = source
	return p, warnings, nil
}

func mihomoVMessTrojan(entry *yaml.Node, fields map[string]*yaml.Node, kind, name, host, endpoint string, port uint64) (profile.Profile, []string, error) {
	var p profile.Profile
	network, err := mihomoOptionalString(fields, "network")
	if err != nil {
		return p, nil, err
	}
	switch network {
	case "", "tcp", "ws":
	case "grpc":
		if kind == "vmess" {
			return p, nil, fmt.Errorf("unsupported Clash/Mihomo vmess network at line %d", entry.Line)
		}
	default:
		return p, nil, fmt.Errorf("unsupported Clash/Mihomo %s network at line %d", kind, entry.Line)
	}
	tls := kind == "trojan"
	if node := fields["tls"]; node != nil {
		if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
			return p, nil, fmt.Errorf("malformed Clash/Mihomo YAML: tls must be a boolean at line %d", node.Line)
		}
		tls, err = strconv.ParseBool(node.Value)
		if err != nil {
			return p, nil, fmt.Errorf("malformed Clash/Mihomo YAML: tls must be a boolean at line %d", node.Line)
		}
	}
	if kind == "trojan" && !tls {
		return p, nil, fmt.Errorf("unsupported Clash/Mihomo trojan without TLS at line %d", entry.Line)
	}
	if fields["sni"] != nil && fields["servername"] != nil {
		return p, nil, fmt.Errorf("unsupported Clash/Mihomo %s ambiguous SNI at line %d", kind, entry.Line)
	}
	sni, err := mihomoOptionalString(fields, "sni")
	if err != nil {
		return p, nil, err
	}
	if fields["servername"] != nil {
		sni, err = mihomoOptionalString(fields, "servername")
		if err != nil {
			return p, nil, err
		}
	}
	fp, err := mihomoOptionalString(fields, "client-fingerprint")
	if err != nil {
		return p, nil, err
	}
	if network == "" {
		network = "tcp"
	}
	query := url.Values{}
	query.Set("type", network)
	if tls {
		query.Set("security", "tls")
	} else {
		query.Set("security", "none")
	}
	if sni != "" {
		query.Set("sni", sni)
	}
	if fp != "" {
		query.Set("fp", fp)
	}
	if err := mihomoExistingALPN(fields["alpn"], query); err != nil {
		return p, nil, err
	}
	if err := mihomoExistingWS(fields["ws-opts"], network, query); err != nil {
		return p, nil, err
	}
	if node := fields["grpc-opts"]; node != nil {
		if network != "grpc" {
			return p, nil, fmt.Errorf("unsupported Clash/Mihomo grpc-opts without grpc at line %d", node.Line)
		}
		if _, err := mihomoStrictOptions(node, map[string]string{"grpc-service-name": "serviceName"}, query); err != nil {
			return p, nil, err
		}
	}
	var warnings []string
	if kind == "trojan" {
		password, err := mihomoRequiredString(fields, "password", entry.Line)
		if err != nil {
			return p, nil, err
		}
		if strings.TrimSpace(password) != password {
			return p, nil, fmt.Errorf("unsupported Clash/Mihomo trojan password whitespace at line %d", entry.Line)
		}
		link := (&url.URL{Scheme: "trojan", User: url.User(strings.ReplaceAll(password, "%", "%25")), Host: endpoint, RawQuery: query.Encode(), Fragment: name}).String()
		p, warnings, err = profile.ImportTrojanURI(link)
		if err == nil && p.UserIdentity != password {
			return profile.Profile{}, nil, fmt.Errorf("invalid Clash/Mihomo trojan profile at line %d", entry.Line)
		}
	} else {
		uuid, err := mihomoRequiredString(fields, "uuid", entry.Line)
		if err != nil {
			return p, nil, err
		}
		cipher, err := mihomoRequiredString(fields, "cipher", entry.Line)
		if err != nil {
			return p, nil, err
		}
		switch cipher {
		case "auto", "aes-128-gcm", "chacha20-poly1305":
		default:
			return p, nil, fmt.Errorf("unsupported Clash/Mihomo vmess cipher at line %d", entry.Line)
		}
		if node := fields["alterId"]; node != nil {
			if node.Kind != yaml.ScalarNode || node.Tag != "!!int" || node.Value != "0" {
				return p, nil, fmt.Errorf("unsupported Clash/Mihomo vmess alterId at line %d", node.Line)
			}
		}
		payload := map[string]string{
			"v": "2", "ps": name, "add": host, "port": strconv.FormatUint(port, 10),
			"id": uuid, "aid": "0", "scy": cipher, "net": network, "type": "none",
			"sni": sni, "fp": fp, "tls": "", "host": query.Get("host"),
			"path": query.Get("path"), "alpn": query.Get("alpn"),
		}
		if tls {
			payload["tls"] = "tls"
		}
		raw, _ := json.Marshal(payload)
		p, warnings, err = profile.ImportVMessURI("vmess://" + base64.StdEncoding.EncodeToString(raw))
	}
	if err != nil {
		return profile.Profile{}, nil, fmt.Errorf("invalid Clash/Mihomo %s profile at line %d", kind, entry.Line)
	}
	return p, warnings, nil
}

func mihomoExistingALPN(node *yaml.Node, query url.Values) error {
	if node == nil {
		return nil
	}
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("malformed Clash/Mihomo YAML: alpn must be a list at line %d", node.Line)
	}
	values := make([]string, 0, len(node.Content))
	for _, child := range node.Content {
		value, err := mihomoString(child, "alpn")
		if err != nil {
			return err
		}
		if value == "" || strings.Contains(value, ",") {
			return fmt.Errorf("unsupported Clash/Mihomo alpn at line %d", child.Line)
		}
		values = append(values, value)
	}
	query.Set("alpn", strings.Join(values, ","))
	return nil
}

func mihomoExistingWS(node *yaml.Node, network string, query url.Values) error {
	if node == nil {
		return nil
	}
	if network != "ws" {
		return fmt.Errorf("unsupported Clash/Mihomo ws-opts without ws at line %d", node.Line)
	}
	opts, err := mihomoMapping(node)
	if err != nil {
		return err
	}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Value != "path" && key.Value != "headers" {
			return fmt.Errorf("unsupported Clash/Mihomo ws-opts option at line %d", key.Line)
		}
	}
	if path := opts["path"]; path != nil {
		value, err := mihomoString(path, "ws-opts.path")
		if err != nil {
			return err
		}
		query.Set("path", value)
	}
	if headers := opts["headers"]; headers != nil {
		h, err := mihomoMapping(headers)
		if err != nil {
			return err
		}
		if len(h) != 1 || h["Host"] == nil {
			return fmt.Errorf("unsupported Clash/Mihomo ws-opts.headers at line %d", headers.Line)
		}
		value, err := mihomoString(h["Host"], "ws-opts.headers.Host")
		if err != nil {
			return err
		}
		query.Set("host", value)
	}
	return nil
}
