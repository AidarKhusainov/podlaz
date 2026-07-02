package api

import (
	"errors"
	"fmt"
	"strings"
)

const (
	ConnectPath        = "/v1/connect"
	DisconnectPath     = "/v1/disconnect"
	XrayPathEnv        = "PODLAZ_XRAY_PATH"
	DefaultXrayCommand = "xray"
)

// ProfileSnapshot is the daemon API's normalized profile payload. It mirrors the
// profile domain model without making the API contract package depend on user
// state storage internals.
type ProfileSnapshot struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Source           string `json:"source"`
	Engine           string `json:"engine"`
	Server           string `json:"server"`
	Port             uint16 `json:"port"`
	Protocol         string `json:"protocol"`
	UserIdentity     string `json:"user_identity,omitempty"`
	Transport        string `json:"transport,omitempty"`
	Security         string `json:"security,omitempty"`
	Encryption       string `json:"encryption,omitempty"`
	Flow             string `json:"flow,omitempty"`
	ServerName       string `json:"server_name,omitempty"`
	ALPN             string `json:"alpn,omitempty"`
	Fingerprint      string `json:"fingerprint,omitempty"`
	Path             string `json:"path,omitempty"`
	HostHeader       string `json:"host_header,omitempty"`
	ServiceName      string `json:"service_name,omitempty"`
	RealityPublicKey string `json:"reality_public_key,omitempty"`
	RealityShortID   string `json:"reality_short_id,omitempty"`
	RealitySpiderX   string `json:"reality_spider_x,omitempty"`
}

// ConnectRequest asks the daemon to start a supervised proxy-only core process.
type ConnectRequest struct {
	Mode    string          `json:"mode"`
	Profile ProfileSnapshot `json:"profile"`
}

// LifecycleResponse is returned by connect and disconnect daemon operations.
type LifecycleResponse struct {
	Connection        string   `json:"connection"`
	Mode              string   `json:"mode,omitempty"`
	ProfileID         string   `json:"profile_id,omitempty"`
	ProfileName       string   `json:"profile_name,omitempty"`
	Proxy             string   `json:"proxy"`
	TUN               string   `json:"tun"`
	Routes            string   `json:"routes,omitempty"`
	DNS               string   `json:"dns,omitempty"`
	Firewall          string   `json:"firewall,omitempty"`
	RuntimeConfigPath string   `json:"runtime_config_path,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
}

func ValidateLifecycleResponse(r LifecycleResponse) error {
	switch {
	case r.Connection == "":
		return errors.New("missing connection field")
	case r.Proxy == "":
		return errors.New("missing proxy field")
	case r.TUN == "":
		return errors.New("missing tun field")
	default:
		return nil
	}
}

func ValidateConnectRequest(r ConnectRequest) error {
	if r.Mode == "" {
		return errors.New("missing mode field")
	}
	if r.Profile.ID == "" {
		return errors.New("missing profile.id field")
	}
	if r.Profile.Name == "" {
		return errors.New("missing profile.name field")
	}
	if r.Profile.Protocol == "" {
		return errors.New("missing profile.protocol field")
	}
	if strings.EqualFold(strings.TrimSpace(r.Profile.Protocol), "xray-json") {
		if strings.TrimSpace(r.Profile.RealitySpiderX) == "" {
			return errors.New("missing profile.reality_spider_x field")
		}
		return nil
	}
	if r.Profile.Server == "" {
		return errors.New("missing profile.server field")
	}
	if r.Profile.Port == 0 {
		return errors.New("missing profile.port field")
	}
	return nil
}

func LifecycleHTTPError(operation string, status string, body string) error {
	if body == "" {
		return fmt.Errorf("daemon %s request failed: unexpected HTTP status %s", operation, status)
	}
	return fmt.Errorf("daemon %s request failed: unexpected HTTP status %s: %s", operation, status, body)
}
