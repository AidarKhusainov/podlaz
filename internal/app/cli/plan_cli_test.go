package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
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
	for _, want := range []string{"Proxy-only plan", "Profile: my-vless-profile", "Mode: proxy-only", "Will listen on SOCKS: 127.0.0.1:1080", "Will not modify TUN, routes, DNS, nftables, or firewall."} {
		assertContains(t, got, want)
	}
	assertNoPlanSecretLeak(t, got)
}

func TestRunCLIPlanProxyOnlyJSONShape(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	profileID := importPlanTestProfile(t, opts)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"plan", "--mode=proxy-only", profileID, "--json"}, &out, opts); err != nil {
		t.Fatalf("plan --json failed: %v", err)
	}
	assertNoPlanSecretLeak(t, out.String())

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode plan JSON: %v", err)
	}
	assertCommonJSON(t, got)
	if got["mode"] != "proxy-only" {
		t.Fatalf("expected mode proxy-only, got %#v", got["mode"])
	}
	plan, ok := got["plan"].(map[string]any)
	if !ok || plan["runtime_config_path"] != "/run/podlaz/generated/xray.json" || plan["starts_xray"] != false || plan["modifies_system_networking"] != false {
		t.Fatalf("unexpected proxy-only plan JSON: %#v", plan)
	}
}

func TestRunCLIPlanTunRendersCompactHumanSummaryByDefault(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := planTestOptions(t, storePath, netsnapshot.FakeResolvedDesktop())
	profileID := importPlanTestProfile(t, opts)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"plan", "--mode", "tun", profileID}, &out, opts); err != nil {
		t.Fatalf("plan --mode tun failed: %v", err)
	}

	got := out.String()
	for _, want := range []string{"podlaz plan", "Profile", "Name       my-vless-profile", "Mode       Full tunnel", "What will happen", "Verify Xray TUN link", "podlaz0, MTU 1500, Xray-owned", "Route traffic through VPN", "Keep VPN server reachable", "203.0.113.10/32 via 192.0.2.1 dev wlp0s20f3", "Configure DNS", "Configure kill switch", "Safety", "No changes were applied.", "Next steps", "Run: plz connect --mode tun", "Details: plz plan --mode tun", "--verbose"} {
		assertContains(t, got, want)
	}
	assertNotContains(t, got, "Create TUN interface")
	for _, forbidden := range []string{"Policy rules:", "Routes:", "DNS plan:", "Firewall rules:", "owner=podlaz:firewall:", "rollback=inet/podlaz"} {
		assertNotContains(t, got, forbidden)
	}
	assertNoPlanSecretLeak(t, got)
}

func TestRunCLIPlanTunPlainOutputUsesASCIIStatusMarkers(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := planTestOptions(t, storePath, netsnapshot.FakeResolvedDesktop())
	profileID := importPlanTestProfile(t, opts)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"plan", "--mode", "tun", profileID, "--plain"}, &out, opts); err != nil {
		t.Fatalf("plan --mode tun --plain failed: %v", err)
	}
	got := out.String()
	assertContains(t, got, "OK Verify Xray TUN link")
	assertNotContains(t, got, "OK Create TUN interface")
	assertNotContains(t, got, "✓")
	assertNotContains(t, got, "✗")
}

func TestRunCLIPlanTunVerboseRendersTechnicalDetails(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := planTestOptions(t, storePath, netsnapshot.FakeResolvedDesktop())
	profileID := importPlanTestProfile(t, opts)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"plan", "--mode", "tun", profileID, "--verbose"}, &out, opts); err != nil {
		t.Fatalf("plan --mode tun --verbose failed: %v", err)
	}

	got := out.String()
	for _, want := range []string{"podlaz TUN plan", "Policy rules:", "Routes:", "DNS plan:", "Firewall plan:", "Firewall chains:", "Firewall rules:", "owner=podlaz:firewall:server-bypass rollback=inet/podlaz/output/server-bypass", "Rollback steps:", "Remove nftables table inet podlaz", "No changes were applied."} {
		assertContains(t, got, want)
	}
	assertNoPlanSecretLeak(t, got)
}

func TestRunCLIPlanTunJSONShapeRemainsStable(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := planTestOptions(t, storePath, netsnapshot.FakeDesktopWithStalepodlazResources())
	profileID := importPlanTestProfile(t, opts)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"plan", "--mode=tun", profileID, "--json"}, &out, opts); err != nil {
		t.Fatalf("plan --mode tun --json failed: %v", err)
	}
	assertNoPlanSecretLeak(t, out.String())

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode TUN plan JSON: %v", err)
	}
	if got["schema_version"] != "v1" || got["mode"] != "tun" || got["status"] != "warn" {
		t.Fatalf("unexpected TUN plan JSON envelope: %#v", got)
	}
	plan, ok := got["plan"].(map[string]any)
	if !ok || plan["tunnel_mode"] != "full-tunnel" || plan["starts_xray"] != false || plan["modifies_system_networking"] != false {
		t.Fatalf("unexpected TUN plan JSON flags: %#v", plan)
	}
	for _, key := range []string{"tun", "routes", "policy_rules", "server_bypass", "dns", "firewall", "snapshot"} {
		if plan[key] == nil {
			t.Fatalf("expected TUN plan JSON key %q, got %#v", key, plan)
		}
	}
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
			if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected usage error containing %q with exit code 2, got %v", tt.wantMessage, err)
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
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "user_identity") {
		t.Fatalf("expected user_identity usage error with exit code 2, got %v", err)
	}
}

func TestRunCLIPlanHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"plan", "--help"}, &out); err != nil {
		t.Fatalf("plan --help failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{"podlaz plan --mode proxy-only", "podlaz plan --mode tun", "compact human summary", "--verbose", "--plain"} {
		assertContains(t, got, want)
	}
}

func planTestOptions(t *testing.T, storePath string, snapshot netsnapshot.Snapshot) options {
	t.Helper()
	return options{
		profileStorePath: storePath,
		systemSnapshot: func(ctx context.Context, opts netsnapshot.Options) netsnapshot.Snapshot {
			if opts.Server != "example.com" {
				t.Fatalf("expected snapshot server example.com, got %q", opts.Server)
			}
			return snapshot
		},
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

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected output to contain %q, got %q", want, got)
	}
}

func assertNotContains(t *testing.T, got, forbidden string) {
	t.Helper()
	if strings.Contains(got, forbidden) {
		t.Fatalf("expected output not to contain %q, got %q", forbidden, got)
	}
}

func assertNoPlanSecretLeak(t *testing.T, got string) {
	t.Helper()
	assertNotContains(t, got, "00000000-0000-0000-0000-000000000001")
	assertNotContains(t, got, "public-key")
}
