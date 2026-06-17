package profile

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ImportShareURI parses a supported Xray-compatible share URI into the normalized profile model.
func ImportShareURI(raw string) (Profile, []string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Profile{}, nil, fmt.Errorf("profile import requires a share URI")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Profile{}, nil, fmt.Errorf("parse share URI: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "vless":
		return importVLESSURI(raw)
	case "vmess":
		return ImportVMessURI(raw)
	case "trojan":
		return ImportTrojanURI(raw)
	case "ss":
		return ImportShadowsocksURI(raw)
	case "":
		return Profile{}, nil, fmt.Errorf("unsupported profile import URI: scheme is required")
	default:
		return Profile{}, nil, fmt.Errorf("unsupported profile import URI scheme %q: supported schemes are vless://, vmess://, trojan://, and ss://", u.Scheme)
	}
}

type vmessShare struct {
	Version  string `json:"v"`
	Name     string `json:"ps"`
	Address  string `json:"add"`
	Port     string `json:"port"`
	ID       string `json:"id"`
	AlterID  any    `json:"aid"`
	Security string `json:"scy"`
	Network  string `json:"net"`
	Type     string `json:"type"`
	Host     string `json:"host"`
	Path     string `json:"path"`
	TLS      string `json:"tls"`
	SNI      string `json:"sni"`
	ALPN     string `json:"alpn"`
	FP       string `json:"fp"`
}

// ImportVMessURI parses a VMess share URI into the normalized profile model.
func ImportVMessURI(raw string) (Profile, []string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Profile{}, nil, fmt.Errorf("profile import requires a VMess share URI")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Profile{}, nil, fmt.Errorf("parse VMess share URI: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "vmess") {
		return Profile{}, nil, fmt.Errorf("unsupported VMess URI scheme %q", u.Scheme)
	}
	payload := strings.TrimPrefix(raw, u.Scheme+"://")
	if payload == "" {
		return Profile{}, nil, fmt.Errorf("invalid VMess URI: encoded JSON payload is required")
	}
	decoded, err := decodeShareBase64(payload)
	if err != nil {
		return Profile{}, nil, fmt.Errorf("invalid VMess URI: payload must be Base64 JSON")
	}
	var share vmessShare
	dec := json.NewDecoder(strings.NewReader(string(decoded)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&share); err != nil {
		return Profile{}, nil, fmt.Errorf("invalid VMess URI: payload JSON is invalid: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err == nil {
		return Profile{}, nil, fmt.Errorf("invalid VMess URI: payload JSON has trailing data")
	}

	host := strings.TrimSpace(share.Address)
	if host == "" {
		return Profile{}, nil, fmt.Errorf("invalid VMess URI: server host is required")
	}
	port, err := parseSharePort(share.Port, "VMess")
	if err != nil {
		return Profile{}, nil, err
	}
	userID := strings.TrimSpace(share.ID)
	if err := validateVMessUserID(userID); err != nil {
		return Profile{}, nil, err
	}
	transport := strings.TrimSpace(share.Network)
	if err := validateShareTransport("VMess", transport); err != nil {
		return Profile{}, nil, err
	}
	security := strings.TrimSpace(share.TLS)
	if err := validateVMessTLS(security); err != nil {
		return Profile{}, nil, err
	}
	encryption := strings.TrimSpace(share.Security)
	if encryption == "" {
		encryption = "auto"
	}
	warnings := vmessWarnings(share)
	name, acceptedName := ProviderProfileDisplayName(share.Name, "vmess", host, port)
	if strings.TrimSpace(share.Name) != "" && !acceptedName {
		warnings = append(warnings, DisplayNameRejectedWarning)
	}
	p := Profile{
		Name:         name,
		Source:       SourceImportedURI,
		Engine:       EngineXray,
		Server:       host,
		Port:         port,
		Protocol:     "vmess",
		UserIdentity: userID,
		Transport:    transport,
		Security:     security,
		Encryption:   encryption,
		ServerName:   strings.TrimSpace(share.SNI),
		ALPN:         strings.TrimSpace(share.ALPN),
		Fingerprint:  strings.TrimSpace(share.FP),
		Path:         strings.TrimSpace(share.Path),
		HostHeader:   strings.TrimSpace(share.Host),
	}
	p.ID = importedShareProfileID(p)
	if err := Validate(p); err != nil {
		return Profile{}, nil, err
	}
	return p, warnings, nil
}

// ImportTrojanURI parses a Trojan share URI into the normalized profile model.
func ImportTrojanURI(raw string) (Profile, []string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Profile{}, nil, fmt.Errorf("profile import requires a Trojan share URI")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Profile{}, nil, fmt.Errorf("parse Trojan share URI: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "trojan") {
		return Profile{}, nil, fmt.Errorf("unsupported Trojan URI scheme %q", u.Scheme)
	}
	if u.User == nil {
		return Profile{}, nil, fmt.Errorf("invalid Trojan URI: password is required")
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		return Profile{}, nil, fmt.Errorf("invalid Trojan URI: password must be the URI user component, not user:password")
	}
	password, err := url.PathUnescape(u.User.Username())
	if err != nil {
		return Profile{}, nil, fmt.Errorf("invalid Trojan URI: password is not valid percent-encoding")
	}
	password = strings.TrimSpace(password)
	if password == "" {
		return Profile{}, nil, fmt.Errorf("invalid Trojan URI: password is required")
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return Profile{}, nil, fmt.Errorf("invalid Trojan URI: server host is required")
	}
	port, err := parseSharePort(u.Port(), "Trojan")
	if err != nil {
		return Profile{}, nil, err
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Profile{}, nil, fmt.Errorf("invalid Trojan URI: query is not valid percent-encoding")
	}
	warnings := unsupportedShareOptionWarnings("Trojan", query, map[string]struct{}{
		"alpn": {}, "allowInsecure": {}, "fp": {}, "host": {}, "path": {}, "security": {}, "serviceName": {}, "sni": {}, "type": {},
	})
	transport, err := shareQueryValue("Trojan", query, "type", false)
	if err != nil {
		return Profile{}, nil, err
	}
	if err := validateShareTransport("Trojan", transport); err != nil {
		return Profile{}, nil, err
	}
	security, err := shareQueryValue("Trojan", query, "security", false)
	if err != nil {
		return Profile{}, nil, err
	}
	if security == "" {
		security = "tls"
	}
	if err := validateTrojanSecurity(security); err != nil {
		return Profile{}, nil, err
	}
	warnings = append(warnings, riskyQueryWarnings("Trojan", query)...)
	p, acceptedName, err := profileFromURIQuery("trojan", u, host, port, password, transport, security, "", query)
	if err != nil {
		return Profile{}, nil, err
	}
	if strings.TrimSpace(u.Fragment) != "" && !acceptedName {
		warnings = append(warnings, DisplayNameRejectedWarning)
	}
	p.ID = importedShareProfileID(p)
	if err := Validate(p); err != nil {
		return Profile{}, nil, err
	}
	return p, warnings, nil
}

// ImportShadowsocksURI parses a Shadowsocks SIP002 share URI into the normalized profile model.
func ImportShadowsocksURI(raw string) (Profile, []string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Profile{}, nil, fmt.Errorf("profile import requires a Shadowsocks share URI")
	}
	if !strings.HasPrefix(strings.ToLower(raw), "ss://") {
		return Profile{}, nil, fmt.Errorf("unsupported Shadowsocks URI scheme")
	}
	u, method, password, err := parseShadowsocksURI(raw)
	if err != nil {
		return Profile{}, nil, err
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return Profile{}, nil, fmt.Errorf("invalid Shadowsocks URI: server host is required")
	}
	port, err := parseSharePort(u.Port(), "Shadowsocks")
	if err != nil {
		return Profile{}, nil, err
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Profile{}, nil, fmt.Errorf("invalid Shadowsocks URI: query is not valid percent-encoding")
	}
	if plugin := strings.TrimSpace(query.Get("plugin")); plugin != "" {
		return Profile{}, nil, fmt.Errorf("unsupported Shadowsocks option %q: plugins are not supported by this profile importer", "plugin")
	}
	warnings := unsupportedShareOptionWarnings("Shadowsocks", query, map[string]struct{}{})
	if !supportedShadowsocksMethod(method) {
		warnings = append(warnings, fmt.Sprintf("Shadowsocks method %q is preserved but may be unsupported by the configured Xray build", method))
	}
	name, acceptedName := ProviderProfileDisplayName(u.Fragment, "shadowsocks", host, port)
	if strings.TrimSpace(u.Fragment) != "" && !acceptedName {
		warnings = append(warnings, DisplayNameRejectedWarning)
	}
	p := Profile{
		Name:         name,
		Source:       SourceImportedURI,
		Engine:       EngineXray,
		Server:       host,
		Port:         port,
		Protocol:     "shadowsocks",
		UserIdentity: method + ":" + password,
		Encryption:   method,
	}
	p.ID = importedShareProfileID(p)
	if err := Validate(p); err != nil {
		return Profile{}, nil, err
	}
	return p, warnings, nil
}

func parseSharePort(value, protocol string) (uint16, error) {
	if value == "" {
		return 0, fmt.Errorf("invalid %s URI: server port is required", protocol)
	}
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil || port == 0 {
		return 0, fmt.Errorf("invalid %s URI: server port must be between 1 and 65535", protocol)
	}
	return uint16(port), nil
}

func decodeShareBase64(raw string) ([]byte, error) {
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, strings.TrimSpace(raw))
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		decoded, err := enc.DecodeString(compact)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("invalid Base64")
}

func validateVMessUserID(id string) error {
	if id == "" {
		return fmt.Errorf("invalid VMess URI: user id is required")
	}
	if strings.ContainsAny(id, " \t\n\r") {
		return fmt.Errorf("invalid VMess URI: user id must not contain whitespace")
	}
	if !uuidPattern.MatchString(id) {
		return fmt.Errorf("invalid VMess URI: user id must be a UUID")
	}
	return nil
}

func validateShareTransport(protocol, transport string) error {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "", "tcp", "raw", "ws", "websocket", "grpc", "httpupgrade", "xhttp", "quic", "kcp":
		return nil
	default:
		return fmt.Errorf("unsupported %s transport %q", protocol, transport)
	}
}

func validateVMessTLS(security string) error {
	switch strings.ToLower(strings.TrimSpace(security)) {
	case "", "none", "tls":
		return nil
	default:
		return fmt.Errorf("unsupported VMess security %q", security)
	}
}

func validateTrojanSecurity(security string) error {
	switch strings.ToLower(strings.TrimSpace(security)) {
	case "", "tls", "none":
		return nil
	default:
		return fmt.Errorf("unsupported Trojan security %q", security)
	}
}

func shareQueryValue(protocol string, query url.Values, key string, required bool) (string, error) {
	values, ok := query[key]
	if !ok {
		if required {
			return "", fmt.Errorf("invalid %s URI: option %q is required", protocol, key)
		}
		return "", nil
	}
	if len(values) != 1 {
		return "", fmt.Errorf("invalid %s URI: option %q must not be repeated", protocol, key)
	}
	value := strings.TrimSpace(values[0])
	if required && value == "" {
		return "", fmt.Errorf("invalid %s URI: option %q requires a value", protocol, key)
	}
	return value, nil
}

func profileFromURIQuery(protocol string, u *url.URL, host string, port uint16, identity, transport, security, encryption string, query url.Values) (Profile, bool, error) {
	serverName, err := shareQueryValue(displayProtocol(protocol), query, "sni", false)
	if err != nil {
		return Profile{}, false, err
	}
	alpn, err := shareQueryValue(displayProtocol(protocol), query, "alpn", false)
	if err != nil {
		return Profile{}, false, err
	}
	fingerprint, err := shareQueryValue(displayProtocol(protocol), query, "fp", false)
	if err != nil {
		return Profile{}, false, err
	}
	path, err := shareQueryValue(displayProtocol(protocol), query, "path", false)
	if err != nil {
		return Profile{}, false, err
	}
	hostHeader, err := shareQueryValue(displayProtocol(protocol), query, "host", false)
	if err != nil {
		return Profile{}, false, err
	}
	serviceName, err := shareQueryValue(displayProtocol(protocol), query, "serviceName", false)
	if err != nil {
		return Profile{}, false, err
	}
	name, acceptedName := ProviderProfileDisplayName(u.Fragment, protocol, host, port)
	return Profile{
		Name:         name,
		Source:       SourceImportedURI,
		Engine:       EngineXray,
		Server:       host,
		Port:         port,
		Protocol:     protocol,
		UserIdentity: identity,
		Transport:    transport,
		Security:     security,
		Encryption:   encryption,
		ServerName:   serverName,
		ALPN:         alpn,
		Fingerprint:  fingerprint,
		Path:         path,
		HostHeader:   hostHeader,
		ServiceName:  serviceName,
	}, acceptedName, nil
}

func displayProtocol(protocol string) string {
	switch protocol {
	case "trojan":
		return "Trojan"
	case "shadowsocks":
		return "Shadowsocks"
	default:
		return strings.ToUpper(protocol[:1]) + protocol[1:]
	}
}

func unsupportedShareOptionWarnings(protocol string, query url.Values, supported map[string]struct{}) []string {
	var unknown []string
	for key := range query {
		if _, ok := supported[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	warnings := make([]string, 0, len(unknown))
	for _, key := range unknown {
		warnings = append(warnings, fmt.Sprintf("unsupported %s option %q ignored", protocol, key))
	}
	return warnings
}

func riskyQueryWarnings(protocol string, query url.Values) []string {
	value := strings.ToLower(strings.TrimSpace(query.Get("allowInsecure")))
	if value == "1" || value == "true" {
		return []string{fmt.Sprintf("%s option %q enables insecure TLS verification", protocol, "allowInsecure")}
	}
	return nil
}

func vmessWarnings(share vmessShare) []string {
	var warnings []string
	if strings.TrimSpace(fmt.Sprint(share.AlterID)) != "" && strings.TrimSpace(fmt.Sprint(share.AlterID)) != "0" {
		warnings = append(warnings, "VMess alterId is preserved in source-compatible identity only partially; non-zero alterId may be unsupported by generated Xray config")
	}
	if strings.TrimSpace(share.Type) != "" && strings.TrimSpace(share.Type) != "none" {
		warnings = append(warnings, fmt.Sprintf("VMess header type %q is preserved as imported metadata only", share.Type))
	}
	return warnings
}

func parseShadowsocksURI(raw string) (*url.URL, string, string, error) {
	rest := strings.TrimPrefix(raw, raw[:5])
	fragment := ""
	if before, after, ok := strings.Cut(rest, "#"); ok {
		rest = before
		fragment = after
	}
	query := ""
	if before, after, ok := strings.Cut(rest, "?"); ok {
		rest = before
		query = after
	}
	if rest == "" {
		return nil, "", "", fmt.Errorf("invalid Shadowsocks URI: credentials and endpoint are required")
	}
	var authority string
	if strings.Contains(rest, "@") {
		userinfo, hostport, _ := strings.Cut(rest, "@")
		decodedUserinfo, err := decodeMaybeBase64(userinfo)
		if err != nil {
			return nil, "", "", fmt.Errorf("invalid Shadowsocks URI: credentials must be method:password")
		}
		method, password, ok := strings.Cut(decodedUserinfo, ":")
		if !ok || strings.TrimSpace(method) == "" || strings.TrimSpace(password) == "" {
			return nil, "", "", fmt.Errorf("invalid Shadowsocks URI: credentials must be method:password")
		}
		authority = url.UserPassword(method, password).String() + "@" + hostport
	} else {
		decoded, err := decodeShareBase64(rest)
		if err != nil {
			return nil, "", "", fmt.Errorf("invalid Shadowsocks URI: payload must be Base64 method:password@host:port")
		}
		authority = string(decoded)
	}
	rebuilt := "ss://" + authority
	if query != "" {
		rebuilt += "?" + query
	}
	if fragment != "" {
		rebuilt += "#" + fragment
	}
	u, err := url.Parse(rebuilt)
	if err != nil {
		return nil, "", "", fmt.Errorf("parse Shadowsocks share URI: %w", err)
	}
	if u.User == nil {
		return nil, "", "", fmt.Errorf("invalid Shadowsocks URI: credentials are required")
	}
	method, err := url.PathUnescape(u.User.Username())
	if err != nil {
		return nil, "", "", fmt.Errorf("invalid Shadowsocks URI: method is not valid percent-encoding")
	}
	password, ok := u.User.Password()
	if !ok {
		return nil, "", "", fmt.Errorf("invalid Shadowsocks URI: credentials must be method:password")
	}
	password, err = url.PathUnescape(password)
	if err != nil {
		return nil, "", "", fmt.Errorf("invalid Shadowsocks URI: password is not valid percent-encoding")
	}
	method = strings.TrimSpace(method)
	password = strings.TrimSpace(password)
	if method == "" {
		return nil, "", "", fmt.Errorf("invalid Shadowsocks URI: method is required")
	}
	if password == "" {
		return nil, "", "", fmt.Errorf("invalid Shadowsocks URI: password is required")
	}
	return u, method, password, nil
}

func decodeMaybeBase64(raw string) (string, error) {
	decoded, err := decodeShareBase64(raw)
	if err == nil && strings.Contains(string(decoded), ":") {
		return string(decoded), nil
	}
	decodedPercent, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	if !strings.Contains(decodedPercent, ":") {
		return "", fmt.Errorf("missing separator")
	}
	return decodedPercent, nil
}

func supportedShadowsocksMethod(method string) bool {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305", "aes-128-gcm", "aes-256-gcm", "chacha20-poly1305", "chacha20-ietf-poly1305", "none":
		return true
	default:
		return false
	}
}

func importedShareProfileID(p Profile) string {
	base := StableImportedProfileIDBase(p.Protocol, p.Server, p.Port)
	if base == "" {
		base = p.Protocol + "-profile"
	}
	fingerprint := strings.Join([]string{
		strings.ToLower(p.Protocol), strings.ToLower(p.Server), strconv.FormatUint(uint64(p.Port), 10), strings.ToLower(p.UserIdentity), strings.ToLower(p.Transport), strings.ToLower(p.Security), strings.ToLower(p.Encryption), strings.ToLower(p.Fingerprint), strings.ToLower(p.ServerName), p.RealityPublicKey, p.RealityShortID,
	}, "\x00")
	sum := sha256.Sum256([]byte(fingerprint))
	return base + "-" + hex.EncodeToString(sum[:])[:10]
}

func validateHostForProfile(host string) error {
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("server host is required")
	}
	if strings.ContainsAny(host, " \t\n\r") {
		return fmt.Errorf("server must not contain whitespace")
	}
	if ip := net.ParseIP(host); ip == nil && !validHostname(host) {
		return fmt.Errorf("server must be a valid hostname or IP address")
	}
	return nil
}
