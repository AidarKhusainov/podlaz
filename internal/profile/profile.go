package profile

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Engine identifies the protocol engine used to establish a connection.
type Engine string

const (
	EngineXray      Engine = "xray"
	EngineAmneziaWG Engine = "amneziawg"
)

// SourceType identifies where a normalized profile came from.
type SourceType string

const (
	SourceManual       SourceType = "manual"
	SourceSubscription SourceType = "subscription"
	SourceImportedFile SourceType = "imported_file"
	SourceImportedURI  SourceType = "imported_uri"
)

// Profile is the normalized internal VPN connection model.
//
// Subscription-specific formats should be parsed into this model before any
// networking plan is created.
type Profile struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Source           SourceType `json:"source"`
	Engine           Engine     `json:"engine"`
	Server           string     `json:"server"`
	Port             uint16     `json:"port"`
	Protocol         string     `json:"protocol"`
	UserIdentity     string     `json:"user_identity,omitempty"`
	Transport        string     `json:"transport,omitempty"`
	Security         string     `json:"security,omitempty"`
	Encryption       string     `json:"encryption,omitempty"`
	Flow             string     `json:"flow,omitempty"`
	ServerName       string     `json:"server_name,omitempty"`
	ALPN             string     `json:"alpn,omitempty"`
	Fingerprint      string     `json:"fingerprint,omitempty"`
	Path             string     `json:"path,omitempty"`
	HostHeader       string     `json:"host_header,omitempty"`
	ServiceName      string     `json:"service_name,omitempty"`
	RealityPublicKey string     `json:"reality_public_key,omitempty"`
	RealityShortID   string     `json:"reality_short_id,omitempty"`
	RealitySpiderX   string     `json:"reality_spider_x,omitempty"`
}

// ValidationError contains clear, stable field-level validation messages.
type ValidationError struct {
	Messages []string
}

func (e ValidationError) Error() string {
	return "invalid profile: " + strings.Join(e.Messages, "; ")
}

// IsValidationError reports whether err is a profile validation failure.
func IsValidationError(err error) bool {
	var validation ValidationError
	return errors.As(err, &validation)
}

// Validate checks the normalized profile fields required before persistence.
func Validate(p Profile) error {
	var messages []string
	if strings.TrimSpace(p.ID) == "" {
		messages = append(messages, "id is required")
	} else if !validID(p.ID) {
		messages = append(messages, "id must contain only lowercase letters, digits, dots, underscores, or hyphens")
	}
	if strings.TrimSpace(p.Name) == "" {
		messages = append(messages, "name is required")
	}
	if p.Source == "" {
		messages = append(messages, "source is required")
	} else if p.Source != SourceManual && p.Source != SourceSubscription && p.Source != SourceImportedFile && p.Source != SourceImportedURI {
		messages = append(messages, fmt.Sprintf("unsupported source %q", p.Source))
	}
	if p.Engine == "" {
		messages = append(messages, "engine is required")
	} else if p.Engine != EngineXray && p.Engine != EngineAmneziaWG {
		messages = append(messages, fmt.Sprintf("unsupported engine %q", p.Engine))
	}
	if strings.TrimSpace(p.Protocol) == "" {
		messages = append(messages, "protocol is required")
	} else if !supportedManualProtocol(p.Protocol) {
		messages = append(messages, fmt.Sprintf("unsupported protocol %q", p.Protocol))
	}
	if strings.TrimSpace(p.Server) == "" {
		messages = append(messages, "server is required")
	} else if strings.ContainsAny(p.Server, " \t\n\r") {
		messages = append(messages, "server must not contain whitespace")
	} else if ip := net.ParseIP(p.Server); ip == nil && !validHostname(p.Server) {
		messages = append(messages, "server must be a valid hostname or IP address")
	}
	if p.Port == 0 {
		messages = append(messages, "port must be between 1 and 65535")
	}
	if p.Source == SourceImportedURI || p.Source == SourceSubscription {
		switch p.Protocol {
		case "vless":
			if strings.TrimSpace(p.UserIdentity) == "" {
				messages = append(messages, "user_identity is required for imported VLESS profiles")
			}
		case "vmess":
			if strings.TrimSpace(p.UserIdentity) == "" {
				messages = append(messages, "user_identity is required for imported VMess profiles")
			}
		case "trojan":
			if strings.TrimSpace(p.UserIdentity) == "" {
				messages = append(messages, "user_identity is required for imported Trojan profiles")
			}
		case "shadowsocks":
			if strings.TrimSpace(p.UserIdentity) == "" {
				messages = append(messages, "user_identity is required for imported Shadowsocks profiles")
			}
		}
	}
	if len(messages) > 0 {
		return ValidationError{Messages: messages}
	}
	return nil
}

// NewManual returns a normalized manual profile with deterministic local ID.
func NewManual(name, server string, port uint16, protocol string) Profile {
	name = strings.TrimSpace(name)
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	return Profile{
		ID:       NormalizeID(name),
		Name:     name,
		Source:   SourceManual,
		Engine:   EngineXray,
		Server:   strings.TrimSpace(server),
		Port:     port,
		Protocol: protocol,
	}
}

// NormalizeID converts a user-visible profile name into a stable local ID.
func NormalizeID(name string) string {
	var b strings.Builder
	lastSeparator := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSeparator = false
		case r == '.' || r == '_' || r == '-':
			if b.Len() > 0 && !lastSeparator {
				b.WriteRune(r)
				lastSeparator = true
			}
		case unicode.IsSpace(r):
			if b.Len() > 0 && !lastSeparator {
				b.WriteByte('-')
				lastSeparator = true
			}
		}
	}
	return strings.Trim(b.String(), ".-_")
}

// SortStable sorts profiles by ID for deterministic output and storage.
func SortStable(profiles []Profile) {
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
}

func supportedManualProtocol(protocol string) bool {
	switch strings.ToLower(protocol) {
	case "vless", "vmess", "trojan", "shadowsocks":
		return true
	default:
		return false
	}
}

func validID(id string) bool {
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

var hostnameLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func validHostname(host string) bool {
	if len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !hostnameLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}
