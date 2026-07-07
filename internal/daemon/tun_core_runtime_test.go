package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
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
	if strings.Contains(text, "podlaz-tun-socks") || strings.Contains(text, `"protocol": "socks"`) {
		t.Fatalf("TUN runtime must not generate private SOCKS adapter config: %s", text)
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
				Address string `json:"address"`
			} `json:"settings"`
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
	if len(config.Outbounds) != 1 || config.Outbounds[0].Settings.Address != "203.0.113.10" {
		t.Fatalf("expected pre-resolved outbound address, got %#v", config.Outbounds)
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
	for _, want := range []string{"TUN mode cannot start because VPN server bypass is unavailable.", "concrete IPv4 server bypass", "No network changes were applied."} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got:\n%s", want, err)
		}
	}
}

func tunRuntimeProfileForTest() profile.Profile {
	return profile.Profile{
		ID:               "tun-runtime-vless",
		Name:             "TUN Runtime VLESS",
		Source:           profile.SourceImportedFile,
		Engine:           profile.EngineXray,
		Server:           "vpn.example",
		Port:             443,
		Protocol:         "vless",
		UserIdentity:     "00000000-0000-0000-0000-000000000701",
		Transport:        "tcp",
		Security:         "reality",
		Encryption:       "none",
		Flow:             "xtls-rprx-vision",
		ServerName:       "vpn.example",
		Fingerprint:      "chrome",
		RealityPublicKey: "public-key-tun",
		RealityShortID:   "abcd",
		RealitySpiderX:   "/",
	}
}
