package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ImportVLESSURI parses a VLESS share URI into the normalized profile model.
func ImportVLESSURI(raw string) (Profile, []string, error) {
	return importVLESSURI(raw)
}

func importVLESSURI(raw string) (Profile, []string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Profile{}, nil, fmt.Errorf("profile import requires a VLESS share URI")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return Profile{}, nil, fmt.Errorf("parse VLESS share URI: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "vless") {
		return Profile{}, nil, fmt.Errorf("unsupported profile import URI scheme %q: only vless:// is implemented", u.Scheme)
	}
	if u.User == nil {
		return Profile{}, nil, fmt.Errorf("invalid VLESS URI: user id is required")
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		return Profile{}, nil, fmt.Errorf("invalid VLESS URI: password component is not supported")
	}
	userID, err := url.PathUnescape(u.User.Username())
	if err != nil {
		return Profile{}, nil, fmt.Errorf("invalid VLESS URI: user id is not valid percent-encoding")
	}
	if err := validateVLESSUserID(userID); err != nil {
		return Profile{}, nil, err
	}

	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return Profile{}, nil, fmt.Errorf("invalid VLESS URI: server host is required")
	}
	port, err := parseVLESSPort(u.Port())
	if err != nil {
		return Profile{}, nil, err
	}

	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Profile{}, nil, fmt.Errorf("invalid VLESS URI: query is not valid percent-encoding")
	}
	warnings := unsupportedVLESSOptionWarnings(query)

	transport, err := vlessQueryValue(query, "type", false)
	if err != nil {
		return Profile{}, nil, err
	}
	if err := validateVLESSTransport(transport); err != nil {
		return Profile{}, nil, err
	}

	security, err := vlessQueryValue(query, "security", false)
	if err != nil {
		return Profile{}, nil, err
	}
	if err := validateVLESSSecurity(security); err != nil {
		return Profile{}, nil, err
	}
	if err := validateVLESSTransportSecurity(transport, security); err != nil {
		return Profile{}, nil, err
	}

	encryption, err := vlessQueryValue(query, "encryption", false)
	if err != nil {
		return Profile{}, nil, err
	}
	if encryption == "" {
		encryption = "none"
	}

	flow, err := vlessQueryValue(query, "flow", false)
	if err != nil {
		return Profile{}, nil, err
	}

	serverName, err := vlessQueryValue(query, "sni", false)
	if err != nil {
		return Profile{}, nil, err
	}
	alpn, err := vlessQueryValue(query, "alpn", false)
	if err != nil {
		return Profile{}, nil, err
	}
	fingerprint, err := vlessQueryValue(query, "fp", false)
	if err != nil {
		return Profile{}, nil, err
	}
	path, err := vlessQueryValue(query, "path", false)
	if err != nil {
		return Profile{}, nil, err
	}
	hostHeader, err := vlessQueryValue(query, "host", false)
	if err != nil {
		return Profile{}, nil, err
	}
	serviceName, err := vlessQueryValue(query, "serviceName", false)
	if err != nil {
		return Profile{}, nil, err
	}
	realityPublicKey, err := vlessQueryValue(query, "pbk", false)
	if err != nil {
		return Profile{}, nil, err
	}
	realityShortID, err := vlessQueryValue(query, "sid", false)
	if err != nil {
		return Profile{}, nil, err
	}
	realitySpiderX, err := vlessQueryValue(query, "spx", false)
	if err != nil {
		return Profile{}, nil, err
	}

	name, acceptedName := ProviderProfileDisplayName(u.Fragment, "vless", host, port)
	if strings.TrimSpace(u.Fragment) != "" && !acceptedName {
		warnings = append(warnings, DisplayNameRejectedWarning)
	}

	p := Profile{
		Name:             name,
		Source:           SourceImportedURI,
		Engine:           EngineXray,
		Server:           host,
		Port:             port,
		Protocol:         "vless",
		UserIdentity:     userID,
		Transport:        transport,
		Security:         security,
		Encryption:       encryption,
		Flow:             flow,
		ServerName:       serverName,
		ALPN:             alpn,
		Fingerprint:      fingerprint,
		Path:             path,
		HostHeader:       hostHeader,
		ServiceName:      serviceName,
		RealityPublicKey: realityPublicKey,
		RealityShortID:   realityShortID,
		RealitySpiderX:   realitySpiderX,
	}
	p.ID = importedVLESSProfileID(p)
	if err := Validate(p); err != nil {
		return Profile{}, nil, err
	}
	return p, warnings, nil
}

func validateVLESSUserID(id string) error {
	id = strings.TrimSpace(id)
	switch {
	case id == "":
		return fmt.Errorf("invalid VLESS URI: user id is required")
	case strings.ContainsAny(id, " \t\n\r"):
		return fmt.Errorf("invalid VLESS URI: user id must not contain whitespace")
	case !uuidPattern.MatchString(id):
		return fmt.Errorf("invalid VLESS URI: user id must be a UUID")
	default:
		return nil
	}
}

func parseVLESSPort(value string) (uint16, error) {
	if value == "" {
		return 0, fmt.Errorf("invalid VLESS URI: server port is required")
	}
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil || port == 0 {
		return 0, fmt.Errorf("invalid VLESS URI: server port must be between 1 and 65535")
	}
	return uint16(port), nil
}

func vlessQueryValue(query url.Values, key string, required bool) (string, error) {
	values, ok := query[key]
	if !ok {
		if required {
			return "", fmt.Errorf("invalid VLESS URI: option %q is required", key)
		}
		return "", nil
	}
	if len(values) != 1 {
		return "", fmt.Errorf("invalid VLESS URI: option %q must not be repeated", key)
	}
	value := strings.TrimSpace(values[0])
	if required && value == "" {
		return "", fmt.Errorf("invalid VLESS URI: option %q requires a value", key)
	}
	return value, nil
}

func validateVLESSTransport(transport string) error {
	if transport == "" {
		return nil
	}
	switch strings.ToLower(transport) {
	case "tcp", "raw", "ws", "grpc", "httpupgrade", "xhttp", "quic", "kcp":
		return nil
	default:
		return fmt.Errorf("unsupported VLESS transport %q", transport)
	}
}

func validateVLESSSecurity(security string) error {
	if security == "" {
		return nil
	}
	switch strings.ToLower(security) {
	case "none", "tls", "reality":
		return nil
	default:
		return fmt.Errorf("unsupported VLESS security %q", security)
	}
}

func validateVLESSTransportSecurity(transport, security string) error {
	if !strings.EqualFold(security, "reality") {
		return nil
	}
	switch strings.ToLower(transport) {
	case "", "tcp", "raw", "xhttp", "grpc":
		return nil
	default:
		return fmt.Errorf("unsupported VLESS transport/security combination: security %q is not compatible with transport %q", security, transport)
	}
}

func unsupportedVLESSOptionWarnings(query url.Values) []string {
	supported := map[string]struct{}{
		"alpn":        {},
		"encryption":  {},
		"flow":        {},
		"fp":          {},
		"host":        {},
		"path":        {},
		"pbk":         {},
		"security":    {},
		"serviceName": {},
		"sid":         {},
		"sni":         {},
		"spx":         {},
		"type":        {},
	}
	var unknown []string
	for key := range query {
		if _, ok := supported[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	warnings := make([]string, 0, len(unknown))
	for _, key := range unknown {
		warnings = append(warnings, fmt.Sprintf("unsupported VLESS option %q ignored", key))
	}
	return warnings
}

func importedVLESSProfileID(p Profile) string {
	base := StableImportedProfileIDBase("vless", p.Server, p.Port)
	if base == "" {
		base = "vless-profile"
	}

	fingerprint := strings.Join([]string{
		"vless",
		strings.ToLower(p.Server),
		strconv.FormatUint(uint64(p.Port), 10),
		strings.ToLower(p.UserIdentity),
		strings.ToLower(p.Transport),
		strings.ToLower(p.Security),
		strings.ToLower(p.Encryption),
		strings.ToLower(p.Fingerprint),
		strings.ToLower(p.ServerName),
		p.RealityPublicKey,
		p.RealityShortID,
	}, "\x00")
	sum := sha256.Sum256([]byte(fingerprint))
	return base + "-" + hex.EncodeToString(sum[:])[:10]
}
