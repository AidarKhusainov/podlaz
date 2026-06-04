package engine

import (
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
