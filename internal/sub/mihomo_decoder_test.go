package sub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const mihomoBasicFixture = `proxies:
  - name: example-vless
    type: vless
    server: vpn.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
    tls: true
    servername: vpn.example.com
    client-fingerprint: chrome
`

func TestMihomoDecodersShareStableProfileIdentity(t *testing.T) {
	local, err := ParseLocalImportContent([]byte(mihomoBasicFixture))
	if err != nil {
		t.Fatalf("local import: %v", err)
	}
	format, remote, err := ParseSubscriptionContent([]byte(mihomoBasicFixture))
	if err != nil {
		t.Fatalf("subscription import: %v", err)
	}
	if format != FormatMihomo || local.Format != profile.LocalImportFormatMihomo {
		t.Fatalf("unexpected formats: remote=%s local=%s", format, local.Format)
	}
	if len(local.Profiles) != 1 || len(remote.Profiles) != 1 {
		t.Fatalf("expected one profile in each path: local=%d remote=%d", len(local.Profiles), len(remote.Profiles))
	}
	lp, rp := local.Profiles[0], remote.Profiles[0]
	if lp.ID != rp.ID || lp.Name != "example-vless" || lp.Engine != profile.EngineXray || lp.Protocol != "vless" {
		t.Fatalf("unexpected local/remote identity or metadata: local=%s remote=%s", lp.ID, rp.ID)
	}
	if lp.Source != profile.SourceImportedFile || rp.Source != profile.SourceSubscription ||
		lp.Security != "tls" || lp.ServerName != "vpn.example.com" || lp.Fingerprint != "chrome" {
		t.Fatalf("unexpected profile mapping: local=%s remote=%s", lp.Security, rp.Security)
	}
}

func TestMihomoSupportedTransportAndRealityMapping(t *testing.T) {
	for _, tt := range []struct {
		name    string
		extra   string
		network string
		security string
		path    string
		host    string
		service string
		key     string
	}{
		{name: "websocket", extra: "    tls: true\n    network: ws\n    ws-opts:\n      path: /ws\n      headers:\n        Host: edge.example.com\n", network: "ws", security: "tls", path: "/ws", host: "edge.example.com"},
		{name: "grpc", extra: "    tls: true\n    network: grpc\n    grpc-opts:\n      grpc-service-name: api\n", network: "grpc", security: "tls", service: "api"},
		{name: "reality", extra: "    tls: true\n    reality-opts:\n      public-key: example-public-key\n      short-id: abcd\n", network: "tcp", security: "reality", key: "example-public-key"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := "proxies:\n  - name: supported\n    type: vless\n    server: vpn.example.com\n    port: 443\n    uuid: 00000000-0000-0000-0000-000000000001\n" + tt.extra
			result, err := ParseLocalImportContent([]byte(data))
			if err != nil || len(result.Profiles) != 1 {
				t.Fatalf("parse: %+v, %v", result, err)
			}
			p := result.Profiles[0]
			if p.Transport != tt.network && !(tt.network == "tcp" && p.Transport == "tcp") ||
				p.Security != tt.security || p.Path != tt.path || p.HostHeader != tt.host ||
				p.ServiceName != tt.service || p.RealityPublicKey != tt.key {
				t.Fatalf("incorrect %s mapping: transport=%s security=%s path=%s host=%s service=%s key_set=%v",
					tt.name, p.Transport, p.Security, p.Path, p.HostHeader, p.ServiceName, p.RealityPublicKey != "")
			}
		})
	}
}

func TestMihomoMalformedAndUnsupportedDoNotFallbackOrLeak(t *testing.T) {
	const secret = "credential-should-not-appear"
	for _, tt := range []struct {
		name, input, want string
	}{
		{name: "truncated recognized YAML", input: "proxies: [", want: "malformed Clash/Mihomo YAML"},
		{name: "invalid mapping", input: "proxies:\n  - missing-mapping", want: "expected mapping"},
		{name: "extra root behavior", input: mihomoBasicFixture + "proxy-groups:\n  - name: " + secret + "\n", want: "unsupported Clash/Mihomo root option"},
		{name: "certificate verify bypass", input: mihomoBasicFixture + "    skip-cert-verify: true\n", want: "unsupported Clash/Mihomo VLESS option"},
		{name: "unknown field with secret key", input: mihomoBasicFixture + "    " + secret + ": ignored\n", want: "unsupported Clash/Mihomo VLESS option"},
		{name: "unsupported encryption", input: mihomoBasicFixture + "    encryption: " + secret + "\n", want: "unsupported Clash/Mihomo VLESS encryption"},
		{name: "unsupported reality knob", input: mihomoBasicFixture + "    reality-opts:\n      public-key: example-key\n      " + secret + ": something\n", want: "unsupported Clash/Mihomo nested option"},
		{name: "wrong bool type", input: mihomoBasicFixture + "    tls: " + secret + "\n", want: "malformed Clash/Mihomo YAML"},
		{name: "duplicate mapping field", input: mihomoBasicFixture + "    server: " + secret + "\n", want: "duplicate field"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, local := range []bool{true, false} {
				var err error
				if local {
					_, err = ParseLocalImportContent([]byte(tt.input))
				} else {
					var format Format
					format, _, err = ParseSubscriptionContent([]byte(tt.input))
					if format != FormatMihomo {
						t.Fatalf("detected %q, want mihomo", format)
					}
				}
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("local=%t err=%v; expected %q", local, err, tt.want)
				}
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error exposed provider material")
				}
			}
		})
	}
}

func TestMihomoUnsupportedProtocolIsReportedWithoutSilentLoss(t *testing.T) {
	input := `proxies:
  - name: unused
    type: trojan
    server: vpn.example.com
    password: example-password
  - name: supported
    type: vless
    server: vpn.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
`
	format, parsed, err := ParseSubscriptionContent([]byte(input))
	if err != nil || format != FormatMihomo || len(parsed.Profiles) != 1 || len(parsed.Unsupported) != 1 {
		t.Fatalf("unexpected parse result: format=%s count=%d unsupported=%d err=%v", format, len(parsed.Profiles), len(parsed.Unsupported), err)
	}
	if !strings.Contains(parsed.Unsupported[0].Message, "only VLESS") || strings.Contains(parsed.Unsupported[0].Message, "example-password") {
		t.Fatalf("unsafe or missing explicit unsupported reason")
	}
}

func TestMihomoRemoteRefreshPreservesCommittedProfilesOnDecoderFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	profiles, subscriptions := newSourceWorkflowStores(t, dir)
	body := mihomoBasicFixture
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	first, err := ImportSource(context.Background(), subscriptions, profiles, server.URL, SourceWorkflowOptions{})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if first.Subscription.Format != FormatMihomo || len(first.Subscription.ProfileIDs) != 1 {
		t.Fatalf("unexpected subscription metadata")
	}
	originalProfiles, err := os.ReadFile(profiles.Path())
	if err != nil {
		t.Fatal(err)
	}
	originalSource, err := os.ReadFile(subscriptions.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"proxies: [",
		mihomoBasicFixture + "    skip-cert-verify: true\n",
	} {
		body = bad
		if _, err := UpdateSource(context.Background(), subscriptions, profiles, first.Subscription.ID, SourceWorkflowOptions{}); err == nil {
			t.Fatal("expected refresh to fail")
		}
		actualProfiles, err := os.ReadFile(profiles.Path())
		if err != nil || string(actualProfiles) != string(originalProfiles) {
			t.Fatal("failed refresh changed committed profile state")
		}
		actualSource, err := os.ReadFile(subscriptions.Path())
		if err != nil || string(actualSource) != string(originalSource) {
			t.Fatal("failed refresh changed committed subscription metadata")
		}
	}
}
