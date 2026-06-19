package daemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestPlanTunCoreRuntimeGeneratesValidatedXrayConfig(t *testing.T) {
	p := profile.Profile{
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
	plan := planner.TunPlan{
		ServerBypass: planner.TunRoutePlan{Destination: "203.0.113.10/32"},
	}

	runtime, err := planTunCoreRuntime(p, "/run/podlaz/generated/xray.json", plan)
	if err != nil {
		t.Fatalf("plan TUN core runtime: %v", err)
	}
	if runtime.RuntimeConfigPath != "/run/podlaz/generated/xray.json" {
		t.Fatalf("unexpected runtime config path: %q", runtime.RuntimeConfigPath)
	}
	if runtime.SOCKSEndpoint == "" || !strings.Contains(runtime.Status, runtime.SOCKSEndpoint) {
		t.Fatalf("expected private SOCKS endpoint in runtime status, got %#v", runtime)
	}
	if len(runtime.Warnings) == 0 {
		t.Fatal("expected TUN runtime warnings to describe connectivity verification")
	}
	var config map[string]any
	if err := json.Unmarshal(runtime.XrayConfig, &config); err != nil {
		t.Fatalf("generated TUN Xray config is not valid JSON: %v", err)
	}
	text := string(runtime.XrayConfig)
	for _, want := range []string{"203.0.113.10", p.UserIdentity, p.Protocol} {
		if !strings.Contains(text, want) {
			t.Fatalf("generated TUN Xray config does not contain %q: %s", want, text)
		}
	}
}
