package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	netsnapshot "github.com/AidarKhusainov/tunwarden/internal/network/snapshot"
)

func TestRunCLIPlanProxyOnlyRendersDryRun(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	profileID := importPlanTestProfile(t, opts)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"plan", "--mode", "proxy-only", profileID}, &out, opts); err != nil {
		t.Fatalf("plan failed: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"Proxy-only plan",
		"Profile: my-vless-profile",
		"Mode: proxy-only",
		"Will generate runtime Xray config: /run/tunwarden/generated/xray.json",
		"Will listen on SOCKS: 127.0.0.1:1080",
		"Will listen on HTTP: 127.0.0.1:8080",
		"Will not modify TUN, routes, DNS, nftables, or firewall.",
		"Will not start Xray or write the generated config in this dry-run.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected plan output to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "00000000-0000-0000-0000-000000000001") || strings.Contains(got, "public-key") {
		t.Fatalf("plan output leaked generated config secret material: %q", got)
	}
}

func TestRunCLIPlanProxyOnlyJSONShape(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	profileID := importPlanTestProfile(t, opts)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"plan", "--mode=proxy-only", profileID, "--json"}, &out, opts); err != nil {
		t.Fatalf("plan --json failed: %v", err)
	}
	if strings.Contains(out.String(), "00000000-0000-0000-0000-000000000001") || strings.Contains(out.String(), "public-key") {
		t.Fatalf("plan --json leaked generated config secret material: %q", out.String())
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode plan JSON: %v", err)
	}
	assertCommonJSON(t, got)
	if got["mode"] != "proxy-only" {
		t.Fatalf("expected mode proxy-only, got %#v", got["mode"])
	}
	plan, ok := got["plan"].(map[string]any)
	if !ok {
		t.Fatalf("expected JSON plan object, got %#v", got["plan"])
	}
	if plan["runtime_config_path"] != "/run/tunwarden/generated/xray.json" || plan["starts_xray"] != false || plan["writes_config"] != false || plan["modifies_system_networking"] != false || plan["system_networking"] == nil {
		t.Fatalf("unexpected plan JSON: %#v", plan)
	}
	if _, ok := plan["profile"].(map[string]any); !ok {
		t.Fatalf("expected proxy-only JSON profile to be preserved, got %#v", plan["profile"])
	}
	listeners, ok := plan["listeners"].([]any)
	if !ok || len(listeners) != 2 {
		t.Fatalf("expected two listeners, got %#v", plan["listeners"])
	}
	steps, ok := got["steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Fatalf("expected non-empty steps, got %#v", got["steps"])
	}
}

func TestRunCLIPlanTunRendersFullTunnelDryRun(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{
		profileStorePath: storePath,
		systemSnapshot: func(ctx context.Context, opts netsnapshot.Options) netsnapshot.Snapshot {
			if opts.Server != "example.com" {
				t.Fatalf("expected snapshot server example.com, got %q", opts.Server)
			}
			return netsnapshot.FakeResolvedDesktop()
		},
	}
	profileID := importPlanTestProfile(t, opts)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"plan", "--mode", "tun", profileID}, &out, opts); err != nil {
		t.Fatalf("plan --mode tun failed: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"TunWarden TUN plan",
		"Mode: full-tunnel",
		"TUN: create tunwarden0",
		"Default traffic: route through tunwarden table",
		"VPN server bypass: add main 203.0.113.10/32 via 192.0.2.1 dev wlp0s20f3",
		"Policy rules:",
		"Routes:",
		"DNS plan:",
		"- backend: systemd-resolved per-link DNS",
		"- target link: tunwarden0",
		"- rollback: restore previous per-link DNS state where possible",
		"Firewall plan:",
		"- create nftables table inet tunwarden",
		"- allow VPN server bypass outside TUN",
		"- allow traffic through tunwarden0",
		"- kill-switch policy: soft",
		"- block non-TUN traffic according to selected kill-switch policy",
		"Firewall chains:",
		"create chain output type filter hook output priority 0 policy accept",
		"Firewall rules:",
		"owner=tunwarden:firewall:server-bypass rollback=inet/tunwarden/output/server-bypass",
		"owner=tunwarden:firewall:tun-egress rollback=inet/tunwarden/output/tun-egress",
		"owner=tunwarden:firewall:kill-switch rollback=inet/tunwarden/output/kill-switch",
		"- rollback: remove inet tunwarden",
		"Rollback steps:",
		"Remove nftables table inet tunwarden",
		"Restore previous systemd-resolved",
		"No changes were applied.",
		"Default IPv4 route: detected, dev wlp0s20f3",
		"DNS mode: systemd-resolved",
		"Stale TunWarden-owned resources: none detected",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected TUN plan output to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "00000000-0000-0000-0000-000000000001") || strings.Contains(got, "public-key") {
		t.Fatalf("TUN plan output leaked generated config secret material: %q", got)
	}
}

func TestRunCLIPlanTunJSONShape(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{
		profileStorePath: storePath,
		systemSnapshot: func(ctx context.Context, opts netsnapshot.Options) netsnapshot.Snapshot {
			return netsnapshot.FakeDesktopWithStaleTunWardenResources()
		},
	}
	profileID := importPlanTestProfile(t, opts)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"plan", "--mode=tun", profileID, "--json"}, &out, opts); err != nil {
		t.Fatalf("plan --mode tun --json failed: %v", err)
	}
	if strings.Contains(out.String(), "00000000-0000-0000-0000-000000000001") || strings.Contains(out.String(), "public-key") {
		t.Fatalf("plan --mode tun --json leaked generated config secret material: %q", out.String())
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode TUN plan JSON: %v", err)
	}
	if got["schema_version"] != "v1" {
		t.Fatalf("expected schema_version v1, got %#v", got["schema_version"])
	}
	if got["mode"] != "tun" || got["status"] != "warn" {
		t.Fatalf("unexpected TUN plan JSON status/mode: %#v", got)
	}
	warnings, ok := got["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Fatalf("expected non-empty warning list for stale-resource TUN snapshot, got %#v", got["warnings"])
	}
	if errors, ok := got["errors"].([]any); !ok || len(errors) != 0 {
		t.Fatalf("expected empty errors, got %#v", got["errors"])
	}
	plan, ok := got["plan"].(map[string]any)
	if !ok {
		t.Fatalf("expected JSON plan object, got %#v", got["plan"])
	}
	if plan["starts_xray"] != false || plan["writes_config"] != false || plan["modifies_system_networking"] != false || plan["tunnel_mode"] != "full-tunnel" || plan["claims_leak_protection"] != false {
		t.Fatalf("unexpected TUN plan safety flags: %#v", plan)
	}
	if _, exists := plan["requires_privileged_executor"]; exists {
		t.Fatalf("requires_privileged_executor is not part of the documented JSON contract: %#v", plan)
	}
	for _, key := range []string{"tun", "routes", "policy_rules", "server_bypass", "dns", "firewall"} {
		if plan[key] == nil {
			t.Fatalf("expected TUN plan JSON key %q, got %#v", key, plan)
		}
	}
	dns, ok := plan["dns"].(map[string]any)
	if !ok || dns["backend"] != "systemd-resolved per-link DNS" || dns["target_link"] != "tunwarden0" {
		t.Fatalf("unexpected TUN DNS plan JSON: %#v", plan["dns"])
	}
	firewall, ok := plan["firewall"].(map[string]any)
	if !ok || firewall["backend"] != "nftables" || firewall["family"] != "inet" || firewall["table"] != "tunwarden" {
		t.Fatalf("unexpected TUN firewall plan JSON: %#v", plan["firewall"])
	}
	chains, ok := firewall["chains"].([]any)
	if !ok || len(chains) == 0 {
		t.Fatalf("expected structured firewall chains JSON, got %#v", firewall["chains"])
	}
	rules, ok := firewall["rules"].([]any)
	if !ok || len(rules) < 3 {
		t.Fatalf("expected structured firewall rules JSON, got %#v", firewall["rules"])
	}
	if !containsJSONRule(rules, "tunwarden:firewall:server-bypass", "inet/tunwarden/output/server-bypass") ||
		!containsJSONRule(rules, "tunwarden:firewall:tun-egress", "inet/tunwarden/output/tun-egress") ||
		!containsJSONRule(rules, "tunwarden:firewall:kill-switch", "inet/tunwarden/output/kill-switch") {
		t.Fatalf("expected owned firewall rules with rollback keys, got %#v", rules)
	}
	killSwitch, ok := firewall["kill_switch"].(map[string]any)
	if !ok || killSwitch["policy"] != "soft" {
		t.Fatalf("unexpected kill-switch JSON: %#v", firewall["kill_switch"])
	}
	snapshot, ok := plan["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("expected TUN snapshot object, got %#v", plan["snapshot"])
	}
	for _, key := range []string{"os", "default_ipv4_route", "default_ipv6_route", "server_route", "dns", "network_manager", "nftables", "tun_devices", "ipv4", "ipv6", "stale_resources"} {
		if snapshot[key] == nil {
			t.Fatalf("expected full snapshot key %q, got %#v", key, snapshot)
		}
	}
	stale, ok := snapshot["stale_resources"].([]any)
	if !ok || len(stale) != 2 {
		t.Fatalf("expected two stale resources in snapshot, got %#v", snapshot["stale_resources"])
	}
}

func containsJSONRule(rules []any, ownership, rollbackKey string) bool {
	for _, value := range rules {
		rule, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if rule["ownership"] == ownership && rule["rollback_key"] == rollbackKey && rule["chain"] != nil && rule["expr"] != nil && rule["action"] != nil {
			return true
		}
	}
	return false
}

func TestRunCLIPlanRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{name: "missing-mode", args: []string{"plan", "test"}, wantMessage: "plan requires --mode proxy-only or tun"},
		{name: "unsupported-mode", args: []string{"plan", "--mode", "wireguard", "test"}, wantMessage: "unsupported plan mode"},
		{name: "missing-profile", args: []string{"plan", "--mode", "proxy-only"}, wantMessage: "plan requires a profile id"},
		{name: "unsupported-flag", args: []string{"plan", "--mode", "proxy-only", "--write", "test"}, wantMessage: "unsupported plan argument"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runWithOptions(context.Background(), tt.args, &bytes.Buffer{}, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
			if err == nil {
				t.Fatalf("expected %v to fail", tt.args)
			}
			if got := ExitCode(err); got != 2 {
				t.Fatalf("expected exit code 2, got %d", got)
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected error containing %q, got %v", tt.wantMessage, err)
			}
		})
	}
}

func TestRunCLIPlanRejectsUnsupportedStoredProfile(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	if err := runWithOptions(context.Background(), []string{"profile", "add", "--name", "manual", "--server", "example.com", "--port", "443", "--protocol", "vless"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("profile add failed: %v", err)
	}

	err := runWithOptions(context.Background(), []string{"plan", "--mode", "proxy-only", "manual"}, &bytes.Buffer{}, opts)
	if err == nil {
		t.Fatal("expected manual profile without VLESS identity to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
	if !strings.Contains(err.Error(), "user_identity") {
		t.Fatalf("expected user_identity error, got %v", err)
	}
}

func TestRunCLIPlanHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"plan", "--help"}, &out); err != nil {
		t.Fatalf("plan --help failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{"tunwarden plan --mode proxy-only", "tunwarden plan --mode tun", "TUN/route/DNS/nftables kill-switch dry-run plan"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected plan help output to contain %q, got %q", want, got)
		}
	}
}

func importPlanTestProfile(t *testing.T, opts options) string {
	t.Helper()
	uri := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=reality&encryption=none&flow=xtls-rprx-vision&sni=www.example.com&fp=chrome&pbk=public-key&sid=abcd&spx=%2F#my-vless-profile"
	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "import", uri}, &out, opts); err != nil {
		t.Fatalf("profile import failed: %v", err)
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.Split(out.String(), "\n")[0], "Imported profile: "))
}
