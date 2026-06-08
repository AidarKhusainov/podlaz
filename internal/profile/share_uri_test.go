package profile

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestImportShareURIValidVMess(t *testing.T) {
	payload := `{"v":"2","ps":"my-vmess-profile","add":"example.com","port":"443","id":"00000000-0000-0000-0000-000000000002","aid":"0","scy":"auto","net":"ws","type":"none","host":"cdn.example.com","path":"/ws","tls":"tls","sni":"example.com","alpn":"h2,http/1.1","fp":"chrome"}`
	uri := "vmess://" + base64.RawStdEncoding.EncodeToString([]byte(payload))

	p, warnings, err := ImportShareURI(uri)
	if err != nil {
		t.Fatalf("import VMess URI: %v", err)
	}
	if !strings.HasPrefix(p.ID, "my-vmess-profile-") || p.Protocol != "vmess" || p.Source != SourceImportedURI || p.Engine != EngineXray {
		t.Fatalf("unexpected normalized VMess metadata: %#v", p)
	}
	if p.Server != "example.com" || p.Port != 443 || p.UserIdentity != "00000000-0000-0000-0000-000000000002" {
		t.Fatalf("unexpected VMess endpoint fields: %#v", p)
	}
	if p.Transport != "ws" || p.Security != "tls" || p.Encryption != "auto" || p.Path != "/ws" || p.HostHeader != "cdn.example.com" || p.ServerName != "example.com" {
		t.Fatalf("unexpected VMess protocol fields: %#v", p)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no VMess warnings, got %#v", warnings)
	}
}

func TestImportShareURIValidTrojan(t *testing.T) {
	uri := "trojan://secret@example.com:443?type=grpc&security=tls&sni=example.com&serviceName=svc&allowInsecure=true#my-trojan-profile"

	p, warnings, err := ImportShareURI(uri)
	if err != nil {
		t.Fatalf("import Trojan URI: %v", err)
	}
	if !strings.HasPrefix(p.ID, "my-trojan-profile-") || p.Protocol != "trojan" {
		t.Fatalf("unexpected normalized Trojan metadata: %#v", p)
	}
	if p.Server != "example.com" || p.Port != 443 || p.UserIdentity != "secret" || p.Transport != "grpc" || p.Security != "tls" || p.ServerName != "example.com" || p.ServiceName != "svc" {
		t.Fatalf("unexpected Trojan fields: %#v", p)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "insecure TLS") {
		t.Fatalf("expected insecure TLS warning, got %#v", warnings)
	}
}

func TestImportShareURIValidShadowsocks(t *testing.T) {
	userinfo := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:secret"))
	uri := "ss://" + userinfo + "@example.com:8388#my-ss-profile"

	p, warnings, err := ImportShareURI(uri)
	if err != nil {
		t.Fatalf("import Shadowsocks URI: %v", err)
	}
	if !strings.HasPrefix(p.ID, "my-ss-profile-") || p.Protocol != "shadowsocks" {
		t.Fatalf("unexpected normalized Shadowsocks metadata: %#v", p)
	}
	if p.Server != "example.com" || p.Port != 8388 || p.Encryption != "aes-256-gcm" || p.UserIdentity != "aes-256-gcm:secret" {
		t.Fatalf("unexpected Shadowsocks fields: %#v", p)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no Shadowsocks warnings, got %#v", warnings)
	}
}

func TestImportShareURIRejectsMalformedInputs(t *testing.T) {
	tests := []struct {
		name        string
		uri         string
		wantMessage string
	}{
		{name: "unsupported-scheme", uri: "hysteria2://example", wantMessage: "supported schemes"},
		{name: "vmess-invalid-base64", uri: "vmess://not-base64", wantMessage: "payload JSON is invalid"},
		{name: "vmess-missing-host", uri: "vmess://" + base64.RawStdEncoding.EncodeToString([]byte(`{"v":"2","port":"443","id":"00000000-0000-0000-0000-000000000002"}`)), wantMessage: "server host is required"},
		{name: "trojan-missing-password", uri: "trojan://example.com:443", wantMessage: "password is required"},
		{name: "shadowsocks-plugin", uri: "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:secret")) + "@example.com:8388?plugin=v2ray-plugin", wantMessage: "plugins are not supported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ImportShareURI(tt.uri)
			if err == nil {
				t.Fatal("expected import to fail")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected error containing %q, got %q", tt.wantMessage, err.Error())
			}
		})
	}
}
