package engine

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

// Imported typed protocols must support both explicit proxy-only and canonical TUN
// rendering. Real data-plane acceptance remains a separate hosted requirement.
func TestImportedXrayProtocolsCanRenderBothConnectionModes(t *testing.T) {
	const uuid = "00000000-0000-0000-0000-000000000002"
	vmess := base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"vmess","add":"example.com","port":"443","id":"` + uuid + `","aid":0,"scy":"auto","net":"tcp","tls":"none"}`))
	for _, tc := range []struct {
		name string
		uri  string
	}{
		{name: "vless", uri: "vless://" + uuid + "@example.com:443?security=none&type=tcp#vless"},
		{name: "vmess", uri: "vmess://" + vmess},
		{name: "trojan", uri: "trojan://example-password@example.com:443?security=tls&type=tcp#trojan"},
		{name: "shadowsocks", uri: "ss://aes-128-gcm:example-password@example.com:443#shadowsocks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _, err := profile.ImportShareURI(tc.uri)
			if err != nil {
				t.Fatalf("import synthetic share URI: %v", err)
			}
			if err := ValidateXrayProxyOnlyProfile(p); err != nil {
				t.Fatalf("imported %s must support explicit proxy-only: %v", tc.name, err)
			}
			if err := ValidateXrayTunProfile(p); err != nil {
				t.Fatalf("imported %s must support canonical full-TUN: %v", tc.name, err)
			}
			if _, err := GenerateXrayProxyOnlyConfig(p, DefaultXrayProxyOnlyConfigOptions()); err != nil {
				t.Fatalf("render proxy-only: %v", err)
			}
			opts := DefaultXrayTunConfigOptions()
			opts.EgressMark = 12345
			raw, err := GenerateXrayTunConfig(p, opts)
			if err != nil {
				t.Fatalf("render marked TUN: %v", err)
			}
			var runtime struct {
				Outbounds []struct {
					Protocol       string         `json:"protocol"`
					StreamSettings map[string]any `json:"streamSettings"`
				} `json:"outbounds"`
			}
			if err := json.Unmarshal(raw, &runtime); err != nil {
				t.Fatalf("decode rendered TUN: %v", err)
			}
			if len(runtime.Outbounds) != 1 || runtime.Outbounds[0].Protocol != tc.name {
				t.Fatalf("incorrect typed outbound protocol: %s", tc.name)
			}
			if tc.name != "vless" {
				sockopt, ok := runtime.Outbounds[0].StreamSettings["sockopt"].(map[string]any)
				if !ok || sockopt["mark"] != float64(opts.EgressMark) {
					t.Fatalf("missing exact protected egress mark for %s", tc.name)
				}
			}
		})
	}
}
