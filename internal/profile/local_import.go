package profile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

const MaxLocalImportSize = 4 * 1024 * 1024

type LocalImportFormat string

const (
	LocalImportFormatXrayJSON      LocalImportFormat = "xray-json"
	LocalImportFormatURIList       LocalImportFormat = "uri-list"
	LocalImportFormatBase64URIList LocalImportFormat = "base64-uri-list"
)

type LocalImportIssue struct {
	Entry   int    `json:"entry"`
	Message string `json:"message"`
}

type LocalImportResult struct {
	Format      LocalImportFormat  `json:"format"`
	Inspected   int                `json:"inspected"`
	Profiles    []Profile          `json:"profiles"`
	Unsupported []LocalImportIssue `json:"unsupported,omitempty"`
	Warnings    []LocalImportIssue `json:"warnings,omitempty"`
}

func ReadLocalImportFile(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("local import path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read local import file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxLocalImportSize+1))
	if err != nil {
		return nil, fmt.Errorf("read local import file: %w", err)
	}
	if len(data) > MaxLocalImportSize {
		return nil, fmt.Errorf("local import file exceeds 4 MiB limit")
	}
	return data, nil
}

func ImportLocalContent(content []byte) (LocalImportResult, error) {
	if len(content) > MaxLocalImportSize {
		return LocalImportResult{}, fmt.Errorf("local import file exceeds 4 MiB limit")
	}
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return LocalImportResult{}, fmt.Errorf("local import file is empty")
	}
	if trimmed[0] == '{' {
		return importXrayJSON(trimmed)
	}
	if json.Valid(trimmed) {
		var value any
		if err := json.Unmarshal(trimmed, &value); err == nil {
			return LocalImportResult{}, fmt.Errorf("unsupported local import JSON top-level type %s; expected Xray JSON object", jsonTopLevelType(value))
		}
	}

	plain, plainErr := importURIList(content, LocalImportFormatURIList)
	if plainErr != nil {
		return LocalImportResult{}, plainErr
	}
	if len(plain.Profiles) > 0 {
		return plain, nil
	}

	decoded, err := decodeLocalImportBase64(content)
	if err == nil {
		base64Result, base64Err := importURIList(decoded, LocalImportFormatBase64URIList)
		if base64Err != nil {
			return LocalImportResult{}, base64Err
		}
		if len(base64Result.Profiles) > 0 {
			return base64Result, nil
		}
		if len(base64Result.Unsupported) > 0 {
			return LocalImportResult{}, fmt.Errorf("local Base64 URI-list contains no supported profiles; first unsupported entry %d: %s", base64Result.Unsupported[0].Entry, base64Result.Unsupported[0].Message)
		}
	}
	if len(plain.Unsupported) > 0 {
		return LocalImportResult{}, fmt.Errorf("local URI-list contains no supported profiles; first unsupported entry %d: %s", plain.Unsupported[0].Entry, plain.Unsupported[0].Message)
	}
	return LocalImportResult{}, fmt.Errorf("local import file contains no supported profiles")
}

func importXrayJSON(content []byte) (LocalImportResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var doc xrayConfig
	if err := decoder.Decode(&doc); err != nil {
		return LocalImportResult{}, fmt.Errorf("malformed Xray JSON config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return LocalImportResult{}, fmt.Errorf("malformed Xray JSON config: trailing data")
	}
	if doc.Outbounds == nil {
		return LocalImportResult{}, fmt.Errorf("unsupported Xray JSON config: outbounds array is required")
	}
	result := LocalImportResult{Format: LocalImportFormatXrayJSON, Inspected: len(doc.Outbounds)}
	seen := map[string]struct{}{}
	for i, outbound := range doc.Outbounds {
		entry := i + 1
		protocol := strings.ToLower(strings.TrimSpace(outbound.Protocol))
		switch protocol {
		case "vless":
			profiles, warnings, err := profilesFromXrayVLESSOutbound(outbound)
			if err != nil {
				result.Unsupported = append(result.Unsupported, LocalImportIssue{Entry: entry, Message: err.Error()})
				continue
			}
			for _, p := range profiles {
				if _, duplicate := seen[p.ID]; duplicate {
					return LocalImportResult{}, fmt.Errorf("duplicate profile id %q in local import", p.ID)
				}
				seen[p.ID] = struct{}{}
				result.Profiles = append(result.Profiles, p)
			}
			for _, warning := range warnings {
				result.Warnings = append(result.Warnings, LocalImportIssue{Entry: entry, Message: warning})
			}
		case "":
			result.Unsupported = append(result.Unsupported, LocalImportIssue{Entry: entry, Message: "outbound protocol is required"})
		default:
			if isIgnoredXrayServiceOutbound(protocol) {
				continue
			}
			result.Unsupported = append(result.Unsupported, LocalImportIssue{Entry: entry, Message: fmt.Sprintf("unsupported outbound protocol %q", protocol)})
		}
	}
	if len(result.Profiles) == 0 {
		if len(result.Unsupported) > 0 {
			return LocalImportResult{}, fmt.Errorf("Xray JSON contains no supported importable outbounds; first unsupported outbound %d: %s", result.Unsupported[0].Entry, result.Unsupported[0].Message)
		}
		return LocalImportResult{}, fmt.Errorf("Xray JSON contains no supported importable outbounds")
	}
	DeduplicateDisplayNames(result.Profiles)
	sort.SliceStable(result.Profiles, func(i, j int) bool { return result.Profiles[i].ID < result.Profiles[j].ID })
	return result, nil
}

func isIgnoredXrayServiceOutbound(protocol string) bool {
	switch protocol {
	case "freedom", "blackhole", "dns", "loopback":
		return true
	default:
		return false
	}
}

func importURIList(content []byte, format LocalImportFormat) (LocalImportResult, error) {
	result := LocalImportResult{Format: format}
	seen := map[string]struct{}{}
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	for i, rawLine := range lines {
		entry := strings.TrimSpace(rawLine)
		if entry == "" {
			continue
		}
		lineNo := i + 1
		result.Inspected++
		p, warnings, err := ImportShareURI(entry)
		if err != nil {
			result.Unsupported = append(result.Unsupported, LocalImportIssue{Entry: lineNo, Message: err.Error()})
			continue
		}
		p.Source = SourceImportedFile
		if err := Validate(p); err != nil {
			result.Unsupported = append(result.Unsupported, LocalImportIssue{Entry: lineNo, Message: err.Error()})
			continue
		}
		if _, duplicate := seen[p.ID]; duplicate {
			return LocalImportResult{}, fmt.Errorf("duplicate profile id %q in local import", p.ID)
		}
		seen[p.ID] = struct{}{}
		result.Profiles = append(result.Profiles, p)
		for _, warning := range warnings {
			result.Warnings = append(result.Warnings, LocalImportIssue{Entry: lineNo, Message: warning})
		}
	}
	DeduplicateDisplayNames(result.Profiles)
	sort.SliceStable(result.Profiles, func(i, j int) bool { return result.Profiles[i].ID < result.Profiles[j].ID })
	return result, nil
}

type xrayConfig struct {
	Outbounds []xrayOutbound `json:"outbounds"`
}

type xrayOutbound struct {
	Protocol       string             `json:"protocol"`
	Tag            string             `json:"tag"`
	Settings       xrayVLESSSettings  `json:"settings"`
	StreamSettings xrayStreamSettings `json:"streamSettings"`
}

type xrayVLESSSettings struct {
	VNext []xrayVLESSVNext `json:"vnext"`
}

type xrayVLESSVNext struct {
	Address string          `json:"address"`
	Port    json.Number     `json:"port"`
	Users   []xrayVLESSUser `json:"users"`
}

type xrayVLESSUser struct {
	ID         string `json:"id"`
	Encryption string `json:"encryption"`
	Flow       string `json:"flow"`
}

type xrayStreamSettings struct {
	Network             string                  `json:"network"`
	Security            string                  `json:"security"`
	TLSSettings         xrayTLSSettings         `json:"tlsSettings"`
	RealitySettings     xrayRealitySettings     `json:"realitySettings"`
	WSSettings          xrayWSSettings          `json:"wsSettings"`
	GRPCSettings        xrayGRPCSettings        `json:"grpcSettings"`
	HTTPUpgradeSettings xrayHTTPUpgradeSettings `json:"httpupgradeSettings"`
	XHTTPSettings       xrayHTTPUpgradeSettings `json:"xhttpSettings"`
}

type xrayTLSSettings struct {
	ServerName  string   `json:"serverName"`
	ALPN        []string `json:"alpn"`
	Fingerprint string   `json:"fingerprint"`
}

type xrayRealitySettings struct {
	ServerName  string `json:"serverName"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId"`
	SpiderX     string `json:"spiderX"`
}

type xrayWSSettings struct {
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
}

type xrayGRPCSettings struct {
	ServiceName string `json:"serviceName"`
}

type xrayHTTPUpgradeSettings struct {
	Path string `json:"path"`
	Host string `json:"host"`
}

func profilesFromXrayVLESSOutbound(outbound xrayOutbound) ([]Profile, []string, error) {
	if len(outbound.Settings.VNext) == 0 {
		return nil, nil, fmt.Errorf("VLESS outbound settings.vnext must contain at least one server")
	}
	transport := normalizeXrayTransport(outbound.StreamSettings.Network)
	security := strings.ToLower(strings.TrimSpace(outbound.StreamSettings.Security))
	if err := validateVLESSTransport(transport); err != nil {
		return nil, nil, err
	}
	if err := validateVLESSSecurity(security); err != nil {
		return nil, nil, err
	}
	if err := validateVLESSTransportSecurity(transport, security); err != nil {
		return nil, nil, err
	}
	var profiles []Profile
	var warnings []string
	warnedRejectedName := false
	for serverIndex, server := range outbound.Settings.VNext {
		host := strings.TrimSpace(server.Address)
		if err := validateHostForProfile(host); err != nil {
			return nil, nil, fmt.Errorf("VLESS vnext %d: %w", serverIndex+1, err)
		}
		port, err := xrayJSONPort(server.Port)
		if err != nil {
			return nil, nil, fmt.Errorf("VLESS vnext %d: %w", serverIndex+1, err)
		}
		if len(server.Users) == 0 {
			return nil, nil, fmt.Errorf("VLESS vnext %d: users must contain at least one user", serverIndex+1)
		}
		name, acceptedName := ProviderProfileDisplayName(outbound.Tag, "vless", host, port)
		if strings.TrimSpace(outbound.Tag) != "" && !acceptedName && !warnedRejectedName {
			warnings = append(warnings, DisplayNameRejectedWarning)
			warnedRejectedName = true
		}
		for userIndex, user := range server.Users {
			userID := strings.TrimSpace(user.ID)
			if err := validateVLESSUserID(userID); err != nil {
				return nil, nil, fmt.Errorf("VLESS vnext %d user %d: %w", serverIndex+1, userIndex+1, err)
			}
			encryption := strings.TrimSpace(user.Encryption)
			if encryption == "" {
				encryption = "none"
			}
			p := Profile{
				Name:         name,
				Source:       SourceImportedFile,
				Engine:       EngineXray,
				Server:       host,
				Port:         port,
				Protocol:     "vless",
				UserIdentity: userID,
				Transport:    transport,
				Security:     security,
				Encryption:   encryption,
				Flow:         strings.TrimSpace(user.Flow),
			}
			applyXrayStreamSettings(&p, outbound.StreamSettings)
			p.ID = importedVLESSProfileID(p)
			if err := Validate(p); err != nil {
				return nil, nil, err
			}
			profiles = append(profiles, p)
		}
	}
	return profiles, warnings, nil
}

func applyXrayStreamSettings(p *Profile, stream xrayStreamSettings) {
	switch strings.ToLower(strings.TrimSpace(stream.Security)) {
	case "tls":
		p.ServerName = strings.TrimSpace(stream.TLSSettings.ServerName)
		p.ALPN = strings.Join(stream.TLSSettings.ALPN, ",")
		p.Fingerprint = strings.TrimSpace(stream.TLSSettings.Fingerprint)
	case "reality":
		p.ServerName = strings.TrimSpace(stream.RealitySettings.ServerName)
		p.Fingerprint = strings.TrimSpace(stream.RealitySettings.Fingerprint)
		p.RealityPublicKey = strings.TrimSpace(stream.RealitySettings.PublicKey)
		p.RealityShortID = strings.TrimSpace(stream.RealitySettings.ShortID)
		p.RealitySpiderX = strings.TrimSpace(stream.RealitySettings.SpiderX)
	}
	switch normalizeXrayTransport(stream.Network) {
	case "ws":
		p.Path = strings.TrimSpace(stream.WSSettings.Path)
		p.HostHeader = strings.TrimSpace(stream.WSSettings.Headers["Host"])
		if p.HostHeader == "" {
			p.HostHeader = strings.TrimSpace(stream.WSSettings.Headers["host"])
		}
	case "grpc":
		p.ServiceName = strings.TrimSpace(stream.GRPCSettings.ServiceName)
	case "httpupgrade":
		p.Path = strings.TrimSpace(stream.HTTPUpgradeSettings.Path)
		p.HostHeader = strings.TrimSpace(stream.HTTPUpgradeSettings.Host)
	case "xhttp":
		p.Path = strings.TrimSpace(stream.XHTTPSettings.Path)
		p.HostHeader = strings.TrimSpace(stream.XHTTPSettings.Host)
	}
}

func normalizeXrayTransport(network string) string {
	network = strings.ToLower(strings.TrimSpace(network))
	if network == "raw" {
		return "tcp"
	}
	return network
}

func xrayJSONPort(value json.Number) (uint16, error) {
	if strings.TrimSpace(value.String()) == "" {
		return 0, fmt.Errorf("server port is required")
	}
	port, err := strconv.ParseUint(value.String(), 10, 16)
	if err != nil || port == 0 {
		return 0, fmt.Errorf("server port must be between 1 and 65535")
	}
	return uint16(port), nil
}

func looksSecretLike(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	if uuidPattern.MatchString(v) {
		return true
	}
	for _, marker := range []string{"token", "password", "passwd", "secret", "priv" + "ate", "author" + "ization", "api" + "_key", "api" + "key"} {
		if strings.Contains(v, marker) {
			return true
		}
	}
	return false
}

func decodeLocalImportBase64(content []byte) ([]byte, error) {
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, string(content))
	if compact == "" {
		return nil, fmt.Errorf("content is empty")
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		decoded, err := enc.DecodeString(compact)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("invalid Base64")
}

func jsonTopLevelType(value any) string {
	switch value.(type) {
	case []any:
		return "array"
	case string:
		return "string"
	case json.Number, float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	case map[string]any:
		return "object"
	default:
		return "value"
	}
}
