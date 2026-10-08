package sub

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"go.yaml.in/yaml/v3"
)

// Recognition does not require valid YAML. A broken recognized document must
// not be interpreted as a Base64 or URI-list payload instead.
var mihomoProxiesHeader = regexp.MustCompile(`(?m)^[ \t]*(?:proxies|'proxies'|"proxies")[ \t]*:`)

func recognizesMihomoYAML(data []byte) bool {
	return mihomoProxiesHeader.Match(data)
}

func parseMihomoYAML(data []byte, source profile.SourceType) (profile.LocalImportResult, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return profile.LocalImportResult{}, fmt.Errorf("malformed Clash/Mihomo YAML")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		return profile.LocalImportResult{}, fmt.Errorf("malformed Clash/Mihomo YAML: multiple documents are unsupported")
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return profile.LocalImportResult{}, fmt.Errorf("malformed Clash/Mihomo YAML: root must be a mapping")
	}
	root, err := mihomoMapping(document.Content[0])
	if err != nil {
		return profile.LocalImportResult{}, err
	}
	for i := 0; i < len(document.Content[0].Content); i += 2 {
		key := document.Content[0].Content[i]
		if key.Value != "proxies" {
			return profile.LocalImportResult{}, fmt.Errorf("unsupported Clash/Mihomo root option at line %d: only a proxies list can be imported", key.Line)
		}
	}
	proxies, ok := root["proxies"]
	if !ok || proxies.Kind != yaml.SequenceNode {
		return profile.LocalImportResult{}, fmt.Errorf("malformed Clash/Mihomo YAML: proxies must be a list")
	}
	result := profile.LocalImportResult{Format: profile.LocalImportFormatMihomo, Inspected: len(proxies.Content)}
	seen := make(map[string]struct{})
	for i, entry := range proxies.Content {
		fields, err := mihomoMapping(entry)
		if err != nil {
			return profile.LocalImportResult{}, err
		}
		kind, err := mihomoRequiredString(fields, "type", entry.Line)
		if err != nil {
			return profile.LocalImportResult{}, err
		}
		if kind != "vless" {
			result.Unsupported = append(result.Unsupported, profile.LocalImportIssue{
				Entry: i + 1, Message: "unsupported Clash/Mihomo proxy protocol: only VLESS is supported",
			})
			continue
		}
		p, warnings, err := mihomoVLESS(entry, fields, source)
		if err != nil {
			return profile.LocalImportResult{}, err
		}
		if _, duplicate := seen[p.ID]; duplicate {
			return profile.LocalImportResult{}, fmt.Errorf("duplicate Clash/Mihomo profile id at entry %d", i+1)
		}
		seen[p.ID] = struct{}{}
		result.Profiles = append(result.Profiles, p)
		for _, warning := range warnings {
			result.Warnings = append(result.Warnings, profile.LocalImportIssue{Entry: i + 1, Message: warning})
		}
	}
	if len(result.Profiles) == 0 {
		if len(result.Unsupported) > 0 {
			return profile.LocalImportResult{}, fmt.Errorf("Clash/Mihomo subscription contains no supported profiles; first unsupported entry %d: %s", result.Unsupported[0].Entry, result.Unsupported[0].Message)
		}
		return profile.LocalImportResult{}, fmt.Errorf("Clash/Mihomo subscription contains no supported profiles")
	}
	profile.DeduplicateDisplayNames(result.Profiles)
	sort.SliceStable(result.Profiles, func(i, j int) bool { return result.Profiles[i].ID < result.Profiles[j].ID })
	return result, nil
}

// Work with YAML nodes rather than a mirrored Mihomo schema. Mapping keys are
// checked explicitly; aliases and duplicate keys cannot obscure semantics.
func mihomoMapping(node *yaml.Node) (map[string]*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("malformed Clash/Mihomo YAML: expected mapping at line %d", node.Line)
	}
	fields := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("malformed Clash/Mihomo YAML: mapping key at line %d must be text", key.Line)
		}
		if _, exists := fields[key.Value]; exists {
			return nil, fmt.Errorf("malformed Clash/Mihomo YAML: duplicate field at line %d", key.Line)
		}
		fields[key.Value] = value
	}
	return fields, nil
}

func mihomoString(node *yaml.Node, field string) (string, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", fmt.Errorf("malformed Clash/Mihomo YAML: %s must be text at line %d", field, node.Line)
	}
	return node.Value, nil
}

func mihomoRequiredString(fields map[string]*yaml.Node, field string, line int) (string, error) {
	value, exists := fields[field]
	if !exists {
		return "", fmt.Errorf("malformed Clash/Mihomo YAML: %s is required at line %d", field, line)
	}
	s, err := mihomoString(value, field)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("malformed Clash/Mihomo YAML: %s must not be empty at line %d", field, value.Line)
	}
	return s, nil
}

func mihomoOptionalString(fields map[string]*yaml.Node, field string) (string, error) {
	if value, exists := fields[field]; exists {
		return mihomoString(value, field)
	}
	return "", nil
}

func mihomoVLESS(entry *yaml.Node, fields map[string]*yaml.Node, source profile.SourceType) (profile.Profile, []string, error) {
	allowed := map[string]bool{
		"name": true, "type": true, "server": true, "port": true, "uuid": true,
		"network": true, "tls": true, "servername": true, "sni": true,
		"flow": true, "encryption": true, "client-fingerprint": true,
		"alpn": true, "reality-opts": true, "ws-opts": true, "grpc-opts": true,
	}
	for i := 0; i < len(entry.Content); i += 2 {
		key := entry.Content[i]
		if !allowed[key.Value] {
			return profile.Profile{}, nil, fmt.Errorf("unsupported Clash/Mihomo VLESS option at line %d", key.Line)
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
	uuid, err := mihomoRequiredString(fields, "uuid", entry.Line)
	if err != nil {
		return profile.Profile{}, nil, err
	}
	portNode, ok := fields["port"]
	if !ok || portNode.Kind != yaml.ScalarNode || portNode.Tag != "!!int" {
		return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: port must be an integer at line %d", entry.Line)
	}
	port, err := strconv.ParseUint(portNode.Value, 10, 16)
	if err != nil || port == 0 {
		return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: port must be between 1 and 65535 at line %d", portNode.Line)
	}
	network, err := mihomoOptionalString(fields, "network")
	if err != nil {
		return profile.Profile{}, nil, err
	}
	switch network {
	case "", "tcp", "ws", "grpc":
	default:
		return profile.Profile{}, nil, fmt.Errorf("unsupported Clash/Mihomo VLESS network at line %d", entry.Line)
	}
	security := "none"
	if tlsNode, exists := fields["tls"]; exists {
		if tlsNode.Kind != yaml.ScalarNode || tlsNode.Tag != "!!bool" {
			return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: tls must be a boolean at line %d", tlsNode.Line)
		}
		enabled, err := strconv.ParseBool(tlsNode.Value)
		if err != nil {
			return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: tls must be a boolean at line %d", tlsNode.Line)
		}
		if enabled {
			security = "tls"
		}
	}
	if _, exists := fields["servername"]; exists {
		if _, duplicate := fields["sni"]; duplicate {
			return profile.Profile{}, nil, fmt.Errorf("unsupported Clash/Mihomo VLESS: ambiguous SNI fields at line %d", entry.Line)
		}
	}
	sni, err := mihomoOptionalString(fields, "servername")
	if err != nil {
		return profile.Profile{}, nil, err
	}
	if _, exists := fields["sni"]; exists {
		sni, err = mihomoOptionalString(fields, "sni")
		if err != nil {
			return profile.Profile{}, nil, err
		}
	}
	query := url.Values{}
	query.Set("type", "tcp")
	if network != "" {
		query.Set("type", network)
	}
	query.Set("security", security)
	query.Set("encryption", "none")
	query.Set("sni", sni)
	for _, pair := range [][2]string{{"flow", "flow"}, {"client-fingerprint", "fp"}} {
		value, err := mihomoOptionalString(fields, pair[0])
		if err != nil {
			return profile.Profile{}, nil, err
		}
		if value != "" {
			query.Set(pair[1], value)
		}
	}
	if v, exists := fields["encryption"]; exists {
		value, err := mihomoString(v, "encryption")
		if err != nil {
			return profile.Profile{}, nil, err
		}
		if value != "" && value != "none" {
			return profile.Profile{}, nil, fmt.Errorf("unsupported Clash/Mihomo VLESS encryption at line %d", v.Line)
		}
	}
	if node, exists := fields["alpn"]; exists {
		if node.Kind != yaml.SequenceNode {
			return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: alpn must be a list at line %d", node.Line)
		}
		values := make([]string, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := mihomoString(child, "alpn")
			if err != nil {
				return profile.Profile{}, nil, err
			}
			if value == "" || strings.Contains(value, ",") {
				return profile.Profile{}, nil, fmt.Errorf("unsupported Clash/Mihomo VLESS alpn value at line %d", child.Line)
			}
			values = append(values, value)
		}
		query.Set("alpn", strings.Join(values, ","))
	}
	if node, exists := fields["reality-opts"]; exists {
		if security != "tls" {
			return profile.Profile{}, nil, fmt.Errorf("unsupported Clash/Mihomo VLESS: reality-opts requires tls at line %d", node.Line)
		}
		options, err := mihomoStrictOptions(node, map[string]string{"public-key": "pbk", "short-id": "sid"}, query)
		if err != nil {
			return profile.Profile{}, nil, err
		}
		if options["public-key"] == nil || strings.TrimSpace(options["public-key"].Value) == "" {
			return profile.Profile{}, nil, fmt.Errorf("malformed Clash/Mihomo YAML: reality-opts.public-key is required at line %d", node.Line)
		}
		query.Set("security", "reality")
	}
	if node, exists := fields["ws-opts"]; exists {
		if network != "ws" {
			return profile.Profile{}, nil, fmt.Errorf("unsupported Clash/Mihomo VLESS: ws-opts requires network ws at line %d", node.Line)
		}
		opts, err := mihomoMapping(node)
		if err != nil {
			return profile.Profile{}, nil, err
		}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Value != "path" && key.Value != "headers" {
				return profile.Profile{}, nil, fmt.Errorf("unsupported Clash/Mihomo ws-opts option at line %d", key.Line)
			}
		}
		if path, ok := opts["path"]; ok {
			value, err := mihomoString(path, "ws-opts.path")
			if err != nil {
				return profile.Profile{}, nil, err
			}
			query.Set("path", value)
		}
		if headers, ok := opts["headers"]; ok {
			h, err := mihomoMapping(headers)
			if err != nil {
				return profile.Profile{}, nil, err
			}
			if len(h) != 1 || h["Host"] == nil {
				return profile.Profile{}, nil, fmt.Errorf("unsupported Clash/Mihomo ws-opts.headers: only Host is supported at line %d", headers.Line)
			}
			value, err := mihomoString(h["Host"], "ws-opts.headers.Host")
			if err != nil {
				return profile.Profile{}, nil, err
			}
			query.Set("host", value)
		}
	}
	if node, exists := fields["grpc-opts"]; exists {
		if network != "grpc" {
			return profile.Profile{}, nil, fmt.Errorf("unsupported Clash/Mihomo VLESS: grpc-opts requires network grpc at line %d", node.Line)
		}
		if _, err := mihomoStrictOptions(node, map[string]string{"grpc-service-name": "serviceName"}, query); err != nil {
			return profile.Profile{}, nil, err
		}
	}
	link := (&url.URL{
		Scheme: "vless", User: url.User(uuid),
		Host:     net.JoinHostPort(host, strconv.FormatUint(port, 10)),
		RawQuery: query.Encode(), Fragment: name,
	}).String()
	p, warnings, err := profile.ImportVLESSURI(link)
	if err != nil {
		// url.Parse and validation errors may contain provider-controlled URL
		// fragments. Keep this boundary error stable and secret-free.
		return profile.Profile{}, nil, fmt.Errorf("invalid Clash/Mihomo VLESS profile at line %d", entry.Line)
	}
	p.Source = source
	return p, warnings, nil
}

func mihomoStrictOptions(node *yaml.Node, allowed map[string]string, query url.Values) (map[string]*yaml.Node, error) {
	fields, err := mihomoMapping(node)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		param, supported := allowed[key.Value]
		if !supported {
			return nil, fmt.Errorf("unsupported Clash/Mihomo nested option at line %d", key.Line)
		}
		value, err := mihomoString(node.Content[i+1], key.Value)
		if err != nil {
			return nil, err
		}
		query.Set(param, value)
	}
	return fields, nil
}
