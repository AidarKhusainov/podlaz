package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/profile"
)

func TestGenerateXrayProxyOnlyConfigMatchesFixture(t *testing.T) {
	got, err := GenerateXrayProxyOnlyConfig(proxyOnlyRealityProfile(), DefaultXrayProxyOnlyConfigOptions())
	if err != nil {
		t.Fatalf("generate proxy-only Xray config: %v", err)
	}

	want, err := os.ReadFile(filepath.Join("testdata", "vless-reality-proxy-only.golden.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("generated config mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestGenerateXrayTunConfigUsesPrivateSocksInbound(t *testing.T) {
	got, err := GenerateXrayTunConfig(proxyOnlyRealityProfile(), DefaultXrayTunConfigOptions())
	if err != nil {
		t.Fatalf("generate TUN-mode Xray config: %v", err)
	}
	var cfg struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Listen   string `json:"listen"`
			Port     uint16 `json:"port"`
			Protocol string `json:"protocol"`
			Settings struct {
				UDP bool `json:"udp"`
			} `json:"settings"`
		} `json:"inbounds"`
		Outbounds []struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(got, &cfg); err != nil {
		t.Fatalf("decode TUN-mode Xray config: %v", err)
	}
	if len(cfg.Inbounds) != 1 {
		t.Fatalf("expected one private TUN adapter inbound, got %#v", cfg.Inbounds)
	}
	inbound := cfg.Inbounds[0]
	if inbound.Tag != "tunwarden-tun-socks" || inbound.Listen != DefaultTunSOCKSListen || inbound.Port != DefaultTunSOCKSPort || inbound.Protocol != "socks" || !inbound.Settings.UDP {
		t.Fatalf("unexpected TUN inbound: %#v", inbound)
	}
	if len(cfg.Outbounds) != 1 || cfg.Outbounds[0].Tag != "tunwarden-tun-proxy" || cfg.Outbounds[0].Protocol != "vless" {
		t.Fatalf("unexpected TUN outbound: %#v", cfg.Outbounds)
	}
}

func TestGenerateXrayProxyOnlyConfigRejectsUnsupportedProfiles(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(profile.Profile) profile.Profile
		wantMessage string
	}{
		{
			name: "non-vless-protocol",
			mutate: func(p profile.Profile) profile.Profile {
				p.Protocol = "vmess"
				return p
			},
			wantMessage: "supports VLESS profiles only",
		},
		{
			name: "unsupported-transport",
			mutate: func(p profile.Profile) profile.Profile {
				p.Transport = "xhttp"
				return p
			},
			wantMessage: "unsupported proxy-only VLESS transport",
		},
		{
			name: "missing-reality-public-key",
			mutate: func(p profile.Profile) profile.Profile {
				p.RealityPublicKey = ""
				return p
			},
			wantMessage: "requires reality_public_key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GenerateXrayProxyOnlyConfig(tt.mutate(proxyOnlyRealityProfile()), DefaultXrayProxyOnlyConfigOptions())
			if err == nil {
				t.Fatal("expected config generation to fail")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected error containing %q, got %v", tt.wantMessage, err)
			}
		})
	}
}

func proxyOnlyRealityProfile() profile.Profile {
	return profile.Profile{
		ID:               "my-vless-profile",
		Name:             "my-vless-profile",
		Source:           profile.SourceImportedURI,
		Engine:           profile.EngineXray,
		Server:           "example.com",
		Port:             443,
		Protocol:         "vless",
		UserIdentity:     "11111111-1111-4111-8111-111111111111",
		Transport:        "tcp",
		Security:         "reality",
		Encryption:       "none",
		Flow:             "xtls-rprx-vision",
		ServerName:       "www.example.com",
		Fingerprint:      "chrome",
		RealityPublicKey: "test-public-key",
		RealityShortID:   "abcd",
		RealitySpiderX:   "/",
	}
}
