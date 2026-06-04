package planner

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/profile"
)

func TestPlanProxyOnlyBuildsInspectableDryRun(t *testing.T) {
	plan, err := PlanProxyOnly(testVLESSProfile())
	if err != nil {
		t.Fatalf("plan proxy-only: %v", err)
	}

	if plan.Mode != ModeProxyOnly {
		t.Fatalf("expected mode %q, got %q", ModeProxyOnly, plan.Mode)
	}
	if plan.RuntimeConfigPath != DefaultRuntimeConfigPath {
		t.Fatalf("unexpected runtime config path: %q", plan.RuntimeConfigPath)
	}
	if len(plan.Listeners) != 2 || plan.Listeners[0].Endpoint() != "127.0.0.1:1080" || plan.Listeners[1].Endpoint() != "127.0.0.1:8080" {
		t.Fatalf("unexpected listeners: %#v", plan.Listeners)
	}
	if len(plan.RollbackSteps) != 0 {
		t.Fatalf("proxy-only dry-run should not need rollback steps, got %#v", plan.RollbackSteps)
	}
	if !strings.Contains(string(plan.XrayConfig), "tunwarden-socks") || !strings.Contains(string(plan.XrayConfig), "tunwarden-proxy") {
		t.Fatalf("expected generated Xray config in plan, got %s", plan.XrayConfig)
	}
	for _, step := range plan.Steps {
		lower := strings.ToLower(step)
		if strings.Contains(lower, "apply route") || strings.Contains(lower, "apply nft") || strings.Contains(lower, "mutate") {
			t.Fatalf("proxy-only plan contains a networking mutation step: %q", step)
		}
	}
}

func TestPlanProxyOnlyRejectsUnsupportedProfileSettings(t *testing.T) {
	p := testVLESSProfile()
	p.Transport = "xhttp"
	_, err := PlanProxyOnly(p)
	if err == nil {
		t.Fatal("expected unsupported transport to fail")
	}
	if !strings.Contains(err.Error(), "unsupported proxy-only VLESS transport") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testVLESSProfile() profile.Profile {
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
