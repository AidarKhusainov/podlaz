package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestPlanTunCoreRuntimeGeneratesValidatedXrayConfig(t *testing.T) {
	p := tunRuntimeProfileForTest()
	plan := planner.TunPlan{
		TunDevice:    planner.TunDevicePlan{Name: "podlaz0", MTU: 1500, Action: "verify"},
		ServerBypass: planner.TunRoutePlan{Destination: "203.0.113.10/32"},
	}

	runtime, err := planTunCoreRuntime(p, "/run/podlaz/generated/xray.json", plan)
	if err != nil {
		t.Fatalf("plan TUN core runtime: %v", err)
	}
	if runtime.RuntimeConfigPath != "/run/podlaz/generated/xray.json" {
		t.Fatalf("unexpected runtime config path: %q", runtime.RuntimeConfigPath)
	}
	if !strings.Contains(runtime.Status, "native podlaz0 TUN inbound") {
		t.Fatalf("expected native Xray TUN status, got %#v", runtime)
	}
	if len(runtime.Warnings) == 0 || !strings.Contains(strings.Join(runtime.Warnings, "\n"), "route, TCP, or DNS verification") {
		t.Fatalf("expected TUN runtime warnings to describe pinned-schema route/DNS verification, got %#v", runtime.Warnings)
	}
	text := string(runtime.XrayConfig)
	legacyTunSocksTag := "podlaz-" + "tun-socks"
	if strings.Contains(text, legacyTunSocksTag) || strings.Contains(text, `"protocol": "socks"`) {
		t.Fatalf("TUN runtime must not generate legacy private SOCKS plumbing: %s", text)
	}

	var config struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
			Settings struct {
				Name      string `json:"name"`
				MTU       int    `json:"MTU"`
				UserLevel int    `json:"userLevel"`
			} `json:"settings"`
		} `json:"inbounds"`
		Outbounds []struct {
			Settings struct {
				VNext []struct {
					Address string `json:"address"`
					Users   []struct {
						ID string `json:"id"`
					} `json:"users"`
				} `json:"vnext"`
			} `json:"settings"`
			StreamSettings struct {
				Security        string `json:"security"`
				RealitySettings struct {
					ServerName  string `json:"serverName"`
					PublicKey   string `json:"publicKey"`
					ShortID     string `json:"shortId"`
					SpiderX     string `json:"spiderX"`
					Fingerprint string `json:"fingerprint"`
				} `json:"realitySettings"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(runtime.XrayConfig, &config); err != nil {
		t.Fatalf("generated TUN Xray config is not valid JSON: %v", err)
	}
	if len(config.Inbounds) != 1 || config.Inbounds[0].Tag != "podlaz-tun" || config.Inbounds[0].Protocol != "tun" {
		t.Fatalf("expected native TUN inbound, got %#v", config.Inbounds)
	}
	if config.Inbounds[0].Settings.Name != "podlaz0" || config.Inbounds[0].Settings.MTU != 1500 {
		t.Fatalf("unexpected TUN inbound settings: %#v", config.Inbounds[0].Settings)
	}
	if len(config.Outbounds) != 1 || len(config.Outbounds[0].Settings.VNext) != 1 || config.Outbounds[0].Settings.VNext[0].Address != "203.0.113.10" {
		t.Fatalf("expected pre-resolved outbound address, got %#v", config.Outbounds)
	}
	if users := config.Outbounds[0].Settings.VNext[0].Users; len(users) != 1 || users[0].ID != p.UserIdentity {
		t.Fatalf("expected TUN VLESS user identity, got %#v", users)
	}
	if config.Outbounds[0].StreamSettings.Security != "reality" {
		t.Fatalf("expected Reality stream settings, got %#v", config.Outbounds[0].StreamSettings)
	}
	reality := config.Outbounds[0].StreamSettings.RealitySettings
	if reality.ServerName != p.ServerName || reality.PublicKey != p.RealityPublicKey || reality.ShortID != p.RealityShortID || reality.SpiderX != p.RealitySpiderX || reality.Fingerprint != p.Fingerprint {
		t.Fatalf("hostname/SNI/Reality semantics must be preserved while only vnext.address is overridden, got %#v", reality)
	}
	for _, want := range []string{p.UserIdentity, p.Protocol} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated TUN Xray config does not contain %q: %s", want, text)
		}
	}
}

func TestPlanTunCoreRuntimeFailsClosedWithoutConcreteServerBypass(t *testing.T) {
	plan := planner.TunPlan{
		TunDevice:    planner.TunDevicePlan{Name: "podlaz0", MTU: 1500, Action: "verify"},
		ServerBypass: planner.TunRoutePlan{Destination: "<server-ip>", Action: "blocked"},
		Snapshot: netsnapshot.Snapshot{
			ServerRoute: netsnapshot.Route{
				Status:    netsnapshot.StatusMissing,
				Interface: "wlan0",
				Detail:    "DNS returned no IPv4 route for vpn.example.test from 192.168.1.20",
				Raw:       "lookup vpn.example.test via 192.168.1.1 failed",
			},
			DefaultIPv4: netsnapshot.Route{Status: netsnapshot.StatusDetected, Interface: "wlan0", Gateway: "192.0.2.1"},
		},
	}

	runtime, err := planTunCoreRuntime(tunRuntimeProfileForTest(), "/run/podlaz/generated/xray.json", plan)
	if err == nil {
		t.Fatal("expected server-bypass failure")
	}
	if len(runtime.XrayConfig) != 0 {
		t.Fatalf("expected no Xray config without concrete server bypass, got %s", runtime.XrayConfig)
	}
	if !isRuntimeUnavailableError(err) {
		t.Fatalf("expected runtime unavailable classification, got %T: %v", err, err)
	}
	if got := daemonAPIHTTPStatusCode(err); got != http.StatusServiceUnavailable {
		t.Fatalf("unexpected HTTP status: got %d want %d", got, http.StatusServiceUnavailable)
	}
	body := err.Error()
	for _, want := range []string{
		"TUN mode cannot start because VPN server bypass is unavailable.",
		"concrete IPv4 server bypass",
		"Diagnostics:",
		"server route status: missing",
		"server route interface: wlan0",
		"server route detail: DNS returned no IPv4 route for vpn.example.test from <private-ipv4>",
		"server route raw: lookup vpn.example.test via <private-ipv4> failed",
		"default IPv4 interface: wlan0",
		"default IPv4 gateway: 192.0.2.1",
		"No network changes were applied.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected error to contain %q, got:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{"192.168.1.20", "192.168.1.1"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("server-bypass diagnostics leaked private IPv4 %q:\n%s", forbidden, body)
		}
	}
}

func tunRuntimeProfileForTest() profile.Profile {
	return profile.Profile{
		ID:               "tun-runtime-vless",
		Name:             "TUN Runtime VLESS",
		Source:           profile.SourceImportedFile,
		Engine:           profile.EngineXray,
		Server:           "vpn.example.test",
		Port:             443,
		Protocol:         "vless",
		UserIdentity:     "00000000-0000-0000-0000-000000000701",
		Transport:        "tcp",
		Security:         "reality",
		Encryption:       "none",
		Flow:             "xtls-rprx-vision",
		ServerName:       "vpn.example.test",
		Fingerprint:      "chrome",
		RealityPublicKey: "public-key-tun",
		RealityShortID:   "abcd",
		RealitySpiderX:   "/",
	}
}
