package sub

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const mihomoHysteria2Fixture = `proxies:
  - name: example-hy2
    type: hysteria2
    server: hy2.example.com
    port: 443
    password: synthetic-example-password
    sni: edge.example.com
    alpn: [h3]
`

func TestMihomoHysteria2PreservesPrivateXrayNativeMaterial(t *testing.T) {
	local, err := ParseLocalImportContent([]byte(mihomoHysteria2Fixture))
	if err != nil {
		t.Fatal(err)
	}
	format, remote, err := ParseSubscriptionContent([]byte(mihomoHysteria2Fixture))
	if err != nil {
		t.Fatal(err)
	}
	if format != FormatMihomo || len(local.Profiles) != 1 || len(remote.Profiles) != 1 {
		t.Fatalf("unexpected import counts or format: %s %d %d", format, len(local.Profiles), len(remote.Profiles))
	}
	p := local.Profiles[0]
	if !profile.IsProviderXrayConfigProfile(p) || p.Engine != profile.EngineXray ||
		p.Source != profile.SourceImportedFile || remote.Profiles[0].Source != profile.SourceSubscription ||
		p.ID != remote.Profiles[0].ID {
		t.Fatal("Hysteria2 did not enter stable native Xray profile boundary")
	}
	var source struct {
		Outbounds []struct {
			Protocol string `json:"protocol"`
			Settings struct {
				Version int    `json:"version"`
				Address string `json:"address"`
				Port    int    `json:"port"`
			} `json:"settings"`
			StreamSettings struct {
				Network  string `json:"network"`
				Security string `json:"security"`
				TLS      struct {
					ServerName string   `json:"serverName"`
					ALPN       []string `json:"alpn"`
				} `json:"tlsSettings"`
				Hysteria struct {
					Version int    `json:"version"`
					Auth    string `json:"auth"`
				} `json:"hysteriaSettings"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(profile.ProviderXrayConfigJSON(p)), &source); err != nil || len(source.Outbounds) != 1 {
		t.Fatalf("invalid private source: %v", err)
	}
	out := source.Outbounds[0]
	if out.Protocol != "hysteria" || out.Settings.Version != 2 || out.Settings.Address != "hy2.example.com" ||
		out.Settings.Port != 443 || out.StreamSettings.Network != "hysteria" ||
		out.StreamSettings.Security != "tls" || out.StreamSettings.Hysteria.Auth != "synthetic-example-password" ||
		out.StreamSettings.Hysteria.Version != 2 || out.StreamSettings.TLS.ServerName != "edge.example.com" ||
		len(out.StreamSettings.TLS.ALPN) != 1 || out.StreamSettings.TLS.ALPN[0] != "h3" {
		t.Fatal("Hysteria2 outbound authentication, endpoint, or TLS translation mismatch")
	}
	renamed := strings.Replace(mihomoHysteria2Fixture, "example-hy2", "renamed", 1)
	other, err := ParseLocalImportContent([]byte(renamed))
	if err != nil || len(other.Profiles) != 1 || other.Profiles[0].ID != p.ID {
		t.Fatal("display rename changed the provider-owned profile identity")
	}
}

func TestMihomoHysteria2RejectsUnsupportedOptionsWithoutLeaking(t *testing.T) {
	const secret = "synthetic-example-password"
	for _, field := range []string{
		"    skip-cert-verify: true\n",
		"    obfs: salamander\n",
		"    obfs-password: sensitive-obfs-password\n",
		"    ports: 443-500\n",
		"    up: 30 Mbps\n",
		"    unexpected-private-option: secret\n",
	} {
		data := mihomoHysteria2Fixture + field
		for _, local := range []bool{true, false} {
			var err error
			if local {
				_, err = ParseLocalImportContent([]byte(data))
			} else {
				_, _, err = ParseSubscriptionContent([]byte(data))
			}
			if err == nil || !strings.Contains(err.Error(), "unsupported Clash/Mihomo Hysteria2 option") {
				t.Fatalf("expected explicit rejection: %v", err)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "sensitive-obfs-password") {
				t.Fatal("unsupported error leaked secrets")
			}
		}
	}
}

func TestMihomoHysteria2UsesCanonicalMarkedNativeTunComposition(t *testing.T) {
	result, err := ParseLocalImportContent([]byte(mihomoHysteria2Fixture))
	if err != nil {
		t.Fatal(err)
	}
	p := result.Profiles[0]
	if err := engine.ValidateProviderXrayTunProfile(p); err != nil {
		t.Fatalf("native TUN profile rejected: %v", err)
	}
	config, err := engine.GenerateXrayTunConfig(p, engine.XrayTunConfigOptions{Name: "podlaz-test", MTU: 1400, EgressMark: 32191})
	if err != nil {
		t.Fatalf("compose marked native TUN: %v", err)
	}
	var doc struct {
		Inbounds []struct {
			Protocol string `json:"protocol"`
		} `json:"inbounds"`
		Outbounds []struct {
			Protocol       string `json:"protocol"`
			StreamSettings struct {
				Sockopt struct {
					Mark uint32 `json:"mark"`
				} `json:"sockopt"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(config, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Inbounds) != 1 || doc.Inbounds[0].Protocol != "tun" ||
		len(doc.Outbounds) != 1 || doc.Outbounds[0].Protocol != "hysteria" ||
		doc.Outbounds[0].StreamSettings.Sockopt.Mark != 32191 {
		t.Fatal("native TUN composition dropped Hysteria2 or marked egress")
	}
}

func TestMihomoHysteria2SubscriptionRefreshIsAtomicAndKeepsSelection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	profiles, subscriptions := newSourceWorkflowStores(t, dir)
	path := filepath.Join(dir, "hy2.yaml")
	if err := os.WriteFile(path, []byte(mihomoHysteria2Fixture), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := ImportSource(context.Background(), subscriptions, profiles, localSourceWorkflowFileURL(path), SourceWorkflowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := profiles.SelectedID()
	if err != nil || selected == "" || len(first.Subscription.ProfileIDs) != 1 {
		t.Fatal("Hysteria2 subscription import lost selected profile")
	}
	beforeProfiles, err := os.ReadFile(profiles.Path())
	if err != nil {
		t.Fatal(err)
	}
	beforeSubscriptions, err := os.ReadFile(subscriptions.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(mihomoHysteria2Fixture+"    obfs: salamander\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSource(context.Background(), subscriptions, profiles, first.Subscription.ID, SourceWorkflowOptions{}); err == nil {
		t.Fatal("unsupported option must fail subscription update")
	}
	afterProfiles, err := os.ReadFile(profiles.Path())
	if err != nil || string(afterProfiles) != string(beforeProfiles) {
		t.Fatal("rejected Hysteria2 refresh mutated committed profile state")
	}
	afterSubscriptions, err := os.ReadFile(subscriptions.Path())
	if err != nil || string(afterSubscriptions) != string(beforeSubscriptions) {
		t.Fatal("rejected Hysteria2 refresh mutated subscription metadata")
	}
	renamed := strings.Replace(mihomoHysteria2Fixture, "example-hy2", "renamed-hy2", 1)
	if err := os.WriteFile(path, []byte(renamed), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := UpdateSource(context.Background(), subscriptions, profiles, first.Subscription.ID, SourceWorkflowOptions{})
	if err != nil {
		t.Fatal("Hysteria2 refresh failed")
	}
	if len(updated.Subscription.ProfileIDs) != 1 || updated.Subscription.ProfileIDs[0] != selected {
		t.Fatal("Hysteria2 display rename retargeted subscription profile identity")
	}
	current, err := profiles.SelectedID()
	if err != nil || current != selected {
		t.Fatal("Hysteria2 subscription rename changed selected profile")
	}
}
