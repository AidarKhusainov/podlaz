package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateAutostartConfigureRequestAcceptsCanonicalConnectMaterial(t *testing.T) {
	req := AutostartConfigureRequest{
		Mode: "tun",
		Profile: ProfileSnapshot{
			ID:           "example-profile",
			Name:         "Example VPN",
			Source:       "manual",
			Engine:       "xray",
			Server:       "vpn.example.com",
			Port:         443,
			Protocol:     "vless",
			UserIdentity: "11111111-1111-1111-1111-111111111111",
			Transport:    "tcp",
			Security:     "tls",
			Encryption:   "none",
		},
	}

	if err := ValidateAutostartConfigureRequest(req); err != nil {
		t.Fatalf("ValidateAutostartConfigureRequest() error = %v", err)
	}
}

func TestValidateAutostartConfigureRequestRejectsIncompleteMaterial(t *testing.T) {
	for name, req := range map[string]AutostartConfigureRequest{
		"missing mode": {
			Profile: ProfileSnapshot{ID: "example-profile", Name: "Example VPN", Protocol: "vless", Server: "vpn.example.com", Port: 443},
		},
		"missing profile": {Mode: "tun"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateAutostartConfigureRequest(req); err == nil {
				t.Fatal("ValidateAutostartConfigureRequest() error = nil, want validation failure")
			}
		})
	}
}

func TestAutostartConfigureRequestDoesNotPersistHandoffPolicy(t *testing.T) {
	req := AutostartConfigureRequest{
		Mode: "proxy-only",
		Profile: ProfileSnapshot{
			ID:       "example-profile",
			Name:     "Example VPN",
			Server:   "vpn.example.com",
			Port:     443,
			Protocol: "vless",
		},
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "handoff") {
		t.Fatalf("autostart request unexpectedly persists handoff policy: %s", encoded)
	}
}

func TestValidateAutostartStatusResponseRequiresDisabledStatusToBeEmpty(t *testing.T) {
	if err := ValidateAutostartStatusResponse(AutostartStatusResponse{Enabled: false, Mode: "tun"}); err == nil {
		t.Fatal("disabled autostart status with mode was accepted")
	}
	if err := ValidateAutostartStatusResponse(AutostartStatusResponse{Enabled: false, ProfileName: "Example VPN"}); err == nil {
		t.Fatal("disabled autostart status with profile name was accepted")
	}
	if err := ValidateAutostartStatusResponse(AutostartStatusResponse{}); err != nil {
		t.Fatalf("disabled empty autostart status error = %v", err)
	}
}

func TestValidateAutostartStatusResponseRequiresEnabledMetadata(t *testing.T) {
	valid := AutostartStatusResponse{Enabled: true, Mode: "tun", ProfileName: "Example VPN"}
	if err := ValidateAutostartStatusResponse(valid); err != nil {
		t.Fatalf("enabled autostart status error = %v", err)
	}

	for _, status := range []AutostartStatusResponse{
		{Enabled: true, ProfileName: "Example VPN"},
		{Enabled: true, Mode: "tun"},
	} {
		if err := ValidateAutostartStatusResponse(status); err == nil {
			t.Fatalf("incomplete enabled status accepted: %+v", status)
		}
	}
}

func TestAutostartAPIPathsAreVersioned(t *testing.T) {
	for name, path := range map[string]string{
		"configure": AutostartConfigurePath,
		"status":    AutostartStatusPath,
	} {
		if !strings.HasPrefix(path, "/v1/") {
			t.Fatalf("%s path = %q, want /v1/ prefix", name, path)
		}
	}
}
