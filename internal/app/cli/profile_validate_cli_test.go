package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

type profileValidateJSON struct {
	SchemaVersion string          `json:"schema_version"`
	Status        string          `json:"status"`
	Warnings      []string        `json:"warnings"`
	Errors        []string        `json:"errors"`
	Profile       profile.Profile `json:"profile"`
	Mode          string          `json:"mode"`
	Backend       string          `json:"backend"`
	Valid         bool            `json:"valid"`
}

func TestRunCLIProfileValidateJSONRedactsRenderableProfile(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	store := mustProfileStore(t, storePath)
	p := renderableVLESSProfile()
	if err := store.Add(p); err != nil {
		t.Fatalf("add profile: %v", err)
	}

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"profile", "validate", p.ID, "--mode", "proxy-only", "--json"}, &out, options{profileStorePath: storePath})
	if err != nil {
		t.Fatalf("profile validate --json failed: %v", err)
	}

	got := decodeProfileValidateJSON(t, out.Bytes())
	if got.SchemaVersion != "v1" || got.Status != "ok" || !got.Valid || got.Mode != "proxy-only" || got.Backend != "xray" {
		t.Fatalf("unexpected profile validate JSON: %#v", got)
	}
	if len(got.Warnings) != 0 || len(got.Errors) != 0 {
		t.Fatalf("expected no warnings/errors, got warnings=%#v errors=%#v", got.Warnings, got.Errors)
	}
	assertNoRawValue(t, out.String(), p.UserIdentity)
}

func TestRunCLIProfileValidateHumanOutputIsStructuredAndRedacted(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	store := mustProfileStore(t, storePath)
	p := renderableVLESSProfile()
	if err := store.Add(p); err != nil {
		t.Fatalf("add profile: %v", err)
	}

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"profile", "validate", p.ID, "--mode", "tun"}, &out, options{profileStorePath: storePath})
	if err != nil {
		t.Fatalf("profile validate failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{"Profile check", "Name       demo vless", "Mode       TUN", "Backend    Xray", "Protocol   VLESS", "Source     Imported URI", "Result", "Profile is valid for TUN mode.", "Next step", "Run: plz plan --mode tun demo-vless"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected human output to contain %q, got %q", want, got)
		}
	}
	assertNoRawValue(t, got, p.UserIdentity)
}

func TestRunCLIProfileValidateHumanOutputDoesNotLeakHostnameBearingIDInNextStep(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	store := mustProfileStore(t, storePath)
	p := renderableVLESSProfile()
	p.ID = "vless-vpn.example.test-443-5659e4f0c6"
	if err := store.Add(p); err != nil {
		t.Fatalf("add profile: %v", err)
	}

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"profile", "validate", p.ID, "--mode", "tun"}, &out, options{profileStorePath: storePath})
	if err != nil {
		t.Fatalf("profile validate failed: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "vpn.example.test") || strings.Contains(got, p.ID) {
		t.Fatalf("expected hostname-bearing profile id to be hidden in next-step output, got %q", got)
	}
	if !strings.Contains(got, "Run: plz plan --mode tun <profile-id>") {
		t.Fatalf("expected safe profile-id placeholder in next-step output, got %q", got)
	}
	assertNoRawValue(t, got, p.UserIdentity)
}

func TestRunCLIProfileValidateHumanFailureUsesPlainBlockedStatus(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	store := mustProfileStore(t, storePath)
	p := renderableVLESSProfile()
	p.ID = "vmess-profile"
	p.Name = "vmess profile"
	p.Protocol = "vmess"
	if err := store.Add(p); err != nil {
		t.Fatalf("add profile: %v", err)
	}

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"profile", "validate", p.ID, "--mode", "tun", "--plain"}, &out, options{profileStorePath: storePath})
	if err == nil || ExitCode(err) != 3 {
		t.Fatalf("expected diagnostic exit code 3, got %v", err)
	}
	got := out.String()
	for _, want := range []string{"Profile check", "Result", "BLOCKED This profile cannot be used in TUN mode.", "Reason", "VLESS profiles only"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected failure output to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "✗") {
		t.Fatalf("plain output must not contain Unicode blocked marker: %q", got)
	}
	assertNoRawValue(t, got, p.UserIdentity)
}

func TestRunCLIProfileValidateRejectsUnsupportedMode(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"profile", "validate", "demo", "--mode", "bad"}, &out, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
	assertUsageError(t, err, out.String(), "unsupported profile validate mode")
}

func TestRunCLIProfileValidateMissingProfileReturnsRuntimeExitCodeAndNoStdout(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"profile", "validate", "missing", "--json"}, &out, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("expected missing profile exit code 1, got %v", err)
	}
	if got := out.String(); got != "" {
		t.Fatalf("expected no stdout for missing profile, got %q", got)
	}
}

func decodeProfileValidateJSON(t *testing.T, data []byte) profileValidateJSON {
	t.Helper()
	var got profileValidateJSON
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode profile validate JSON: %v\n%s", err, string(data))
	}
	return got
}

func mustProfileStore(t *testing.T, path string) profile.Store {
	t.Helper()
	store, err := profile.NewStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

func assertNoRawValue(t *testing.T, output, raw string) {
	t.Helper()
	if strings.Contains(output, raw) {
		t.Fatalf("expected output not to contain raw value %q, got %q", raw, output)
	}
}

func renderableVLESSProfile() profile.Profile {
	return profile.Profile{
		ID:           "demo-vless",
		Name:         "demo vless",
		Source:       profile.SourceImportedURI,
		Engine:       profile.EngineXray,
		Server:       "example.com",
		Port:         443,
		Protocol:     "vless",
		UserIdentity: "11111111-2222-3333-4444-555555555555",
		Transport:    "tcp",
		Security:     "none",
		Encryption:   "none",
	}
}
