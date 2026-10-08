package sub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const mihomoExistingFixture = `proxies:
  - name: vmess
    type: vmess
    server: vpn.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000002
    alterId: 0
    cipher: auto
    tls: true
    servername: vpn.example.com
    network: ws
    ws-opts:
      path: /api
      headers:
        Host: edge.example.com
  - name: trojan
    type: trojan
    server: vpn.example.com
    port: 443
    password: example-trojan-password
    sni: vpn.example.com
    network: grpc
    grpc-opts:
      grpc-service-name: api
  - name: shadowsocks
    type: ss
    server: vpn.example.com
    port: 8388
    cipher: aes-128-gcm
    password: example-shadowsocks-password
  - name: vless
    type: vless
    server: vpn.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
    tls: true
`

func TestMihomoExistingProtocolsLocalAndSubscriptionImport(t *testing.T) {
	local, err := ParseLocalImportContent([]byte(mihomoExistingFixture))
	if err != nil {
		t.Fatal(err)
	}
	format, remote, err := ParseSubscriptionContent([]byte(mihomoExistingFixture))
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatMihomo || len(local.Profiles) != 4 || len(remote.Profiles) != 4 {
		t.Fatalf("unexpected import count/format: %s %d %d", format, len(local.Profiles), len(remote.Profiles))
	}
	byProtocol := map[string]profile.Profile{}
	for i, p := range local.Profiles {
		if p.ID != remote.Profiles[i].ID {
			t.Fatal("local/remote identity mismatch")
		}
		byProtocol[p.Protocol] = p
		if p.Source != profile.SourceImportedFile {
			t.Fatalf("unexpected source: %s", p.Source)
		}
	}
	if byProtocol["vmess"].Transport != "ws" || byProtocol["vmess"].Encryption != "auto" || byProtocol["vmess"].Path != "/api" ||
		byProtocol["trojan"].Security != "tls" || byProtocol["trojan"].ServiceName != "api" ||
		byProtocol["shadowsocks"].Encryption != "aes-128-gcm" || byProtocol["vless"].Protocol != "vless" {
		t.Fatal("incorrect protocol mapping")
	}
}

func TestMihomoExistingProtocolsRejectBehaviorChangingOptions(t *testing.T) {
	const secret = "provider-secret-must-stay-redacted"
	for _, tt := range []struct {
		name, protocol, extra string
	}{
		{"vmess legacy", "vmess", "    alterId: 1\n"},
		{"vmess header", "vmess", "    header: " + secret + "\n"},
		{"vmess packet", "vmess", "    packet-encoding: xudp\n"},
		{"vmess cipher", "vmess", "    cipher: unsupported\n"},
		{"trojan tls", "trojan", "    tls: false\n"},
		{"trojan insecure", "trojan", "    skip-cert-verify: true\n"},
		{"trojan plugin", "trojan", "    ss-opts:\n      enabled: true\n"},
		{"ss plugin", "ss", "    plugin: obfs\n"},
		{"ss cipher", "ss", "    cipher: unsupported\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var base string
			switch tt.protocol {
			case "vmess":
				base = "    uuid: 00000000-0000-0000-0000-000000000002\n    cipher: auto\n"
			case "trojan", "ss":
				base = "    password: example-password\n"
				if tt.protocol == "ss" {
					base += "    cipher: aes-128-gcm\n"
				}
			}
			if strings.Contains(tt.extra, "    cipher:") {
				base = strings.ReplaceAll(base, "    cipher: auto\n", "")
				base = strings.ReplaceAll(base, "    cipher: aes-128-gcm\n", "")
			}
			input := "proxies:\n  - name: example\n    type: " + tt.protocol + "\n    server: vpn.example.com\n    port: 443\n" + base + tt.extra
			_, err := ParseLocalImportContent([]byte(input))
			if err == nil {
				t.Fatal("expected strict rejection")
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "example-password") {
				t.Fatal("error leaked credentials")
			}
		})
	}
}

func TestMihomoExistingProtocolsRejectDuplicateIdentity(t *testing.T) {
	extra := `  - name: vmess-copy
    type: vmess
    server: vpn.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000002
    alterId: 0
    cipher: auto
    tls: true
    servername: vpn.example.com
    network: ws
    ws-opts:
      path: /api
      headers:
        Host: edge.example.com
`
	input := strings.Replace(mihomoExistingFixture, "  - name: trojan", extra+"  - name: trojan", 1)
	_, err := ParseLocalImportContent([]byte(input))
	if err == nil || !strings.Contains(err.Error(), "duplicate Clash/Mihomo profile id") {
		t.Fatalf("expected duplicate rejection: %v", err)
	}
}

func TestMihomoExistingProtocolsHTTPRefreshAtomicityAndSelection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	profiles, subscriptions := newSourceWorkflowStores(t, dir)
	body := mihomoExistingFixture
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	first, err := ImportSource(context.Background(), subscriptions, profiles, server.URL, SourceWorkflowOptions{})
	if err != nil {
		t.Fatalf("initial HTTP import: %v", err)
	}
	if len(first.Subscription.ProfileIDs) != 4 {
		t.Fatalf("expected four imported profiles, got %d", len(first.Subscription.ProfileIDs))
	}
	selectedProfile, err := profiles.Select("vmess")
	if err != nil {
		t.Fatalf("select VMess profile: %v", err)
	}
	originalProfiles, err := os.ReadFile(profiles.Path())
	if err != nil {
		t.Fatal(err)
	}
	originalSubscription, err := os.ReadFile(subscriptions.Path())
	if err != nil {
		t.Fatal(err)
	}
	body = strings.Replace(mihomoExistingFixture, "    cipher: auto", "    packet-encoding: xudp\n    cipher: auto", 1)
	if _, err := UpdateSource(context.Background(), subscriptions, profiles, first.Subscription.ID, SourceWorkflowOptions{}); err == nil {
		t.Fatal("expected unsupported option to abort refresh")
	}
	currentProfiles, err := os.ReadFile(profiles.Path())
	if err != nil || string(currentProfiles) != string(originalProfiles) {
		t.Fatal("failed refresh changed stored profiles")
	}
	currentSubscription, err := os.ReadFile(subscriptions.Path())
	if err != nil || string(currentSubscription) != string(originalSubscription) {
		t.Fatal("failed refresh changed stored subscription")
	}
	body = strings.Replace(mihomoExistingFixture, "  - name: vmess", "  - name: renamed-vmess", 1)
	updated, err := UpdateSource(context.Background(), subscriptions, profiles, first.Subscription.ID, SourceWorkflowOptions{})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(updated.Subscription.ProfileIDs) != len(first.Subscription.ProfileIDs) {
		t.Fatal("refresh changed profile count")
	}
	for i, id := range first.Subscription.ProfileIDs {
		if updated.Subscription.ProfileIDs[i] != id {
			t.Fatal("rename changed stable profile identity")
		}
	}
	selectedID, err := profiles.SelectedID()
	if err != nil || selectedID != selectedProfile.ID {
		t.Fatalf("refresh changed selected profile: %s, %v", selectedID, err)
	}
}

func TestMihomoExistingProtocolsMatchShareURIConnectCapability(t *testing.T) {
	result, err := ParseLocalImportContent([]byte(mihomoExistingFixture))
	if err != nil {
		t.Fatal(err)
	}
	vmessJSON, err := json.Marshal(map[string]string{
		"v": "2", "ps": "vmess", "add": "vpn.example.com", "port": "443",
		"id": "00000000-0000-0000-0000-000000000002", "aid": "0",
		"scy": "auto", "net": "ws", "tls": "tls", "sni": "vpn.example.com",
		"path": "/api", "host": "edge.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	share := map[string]string{
		"vmess":       "vmess://" + base64.StdEncoding.EncodeToString(vmessJSON),
		"trojan":      "trojan://example-trojan-password@vpn.example.com:443?security=tls&type=grpc&serviceName=api&sni=vpn.example.com#trojan",
		"shadowsocks": "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:example-shadowsocks-password")) + "@vpn.example.com:8388#shadowsocks",
	}
	for _, imported := range result.Profiles {
		link, ok := share[imported.Protocol]
		if !ok {
			continue
		}
		equivalent, _, err := profile.ImportShareURI(link)
		if err != nil {
			t.Fatalf("import equivalent %s share URI: %v", imported.Protocol, err)
		}
		if imported.Protocol != equivalent.Protocol || imported.UserIdentity != equivalent.UserIdentity ||
			imported.Server != equivalent.Server || imported.Port != equivalent.Port {
			t.Fatalf("%s import differs from equivalent share profile", imported.Protocol)
		}
		for _, validate := range []func(profile.Profile) error{
			engine.ValidateXrayProxyOnlyProfile,
			engine.ValidateXrayTunProfile,
		} {
			a, b := validate(imported), validate(equivalent)
			if (a == nil) != (b == nil) {
				t.Fatalf("%s YAML/share runtime acceptance differs", imported.Protocol)
			}
			if a == nil {
				t.Fatalf("%s unexpectedly accepted by VLESS-only runtime; verify actual connectivity first", imported.Protocol)
			}
		}
	}
}

func TestMihomoExistingProtocolsPreservePercentInPasswords(t *testing.T) {
	const password = "pa%2Fss%word"
	for _, tc := range []struct {
		protocol, fields, want string
	}{
		{"trojan", "    password: " + password + "\n", password},
		{"ss", "    cipher: aes-128-gcm\n    password: " + password + "\n", "aes-128-gcm:" + password},
	} {
		t.Run(tc.protocol, func(t *testing.T) {
			input := "proxies:\n  - name: example\n    type: " + tc.protocol + "\n    server: vpn.example.com\n    port: 443\n" + tc.fields
			got, err := ParseLocalImportContent([]byte(input))
			if err != nil || len(got.Profiles) != 1 {
				t.Fatalf("import failed: count=%d err=%v", len(got.Profiles), err)
			}
			if got.Profiles[0].UserIdentity != tc.want {
				t.Fatal("Mihomo password changed during import")
			}
		})
	}
}

func TestMihomoExistingProtocolsRejectLossyPasswordNormalization(t *testing.T) {
	for _, protocol := range []string{"trojan", "ss"} {
		t.Run(protocol, func(t *testing.T) {
			fields := "    password: \" padded-secret \"\n"
			if protocol == "ss" {
				fields += "    cipher: aes-128-gcm\n"
			}
			input := "proxies:\n  - name: example\n    type: " + protocol + "\n    server: vpn.example.com\n    port: 443\n" + fields
			if _, err := ParseLocalImportContent([]byte(input)); err == nil || strings.Contains(err.Error(), "padded-secret") {
				t.Fatal("expected redacted rejection of lossy password")
			}
		})
	}
}
