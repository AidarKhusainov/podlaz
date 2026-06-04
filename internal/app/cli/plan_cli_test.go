package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
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
	if plan["runtime_config_path"] != "/run/tunwarden/generated/xray.json" || plan["starts_xray"] != false || plan["writes_config"] != false || plan["modifies_system_networking"] != false {
		t.Fatalf("unexpected plan JSON: %#v", plan)
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

func TestRunCLIPlanRejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{name: "missing-mode", args: []string{"plan", "test"}, wantMessage: "plan requires --mode proxy-only"},
		{name: "unsupported-mode", args: []string{"plan", "--mode", "tun", "test"}, wantMessage: "unsupported plan mode"},
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
	if got := out.String(); !strings.Contains(got, "Usage:\n  tunwarden plan --mode proxy-only") || !strings.Contains(got, "does not start Xray") {
		t.Fatalf("expected plan help output, got %q", got)
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
