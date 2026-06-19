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
	if got.SchemaVersion != "v1" {
		t.Fatalf("expected schema_version v1, got %q", got.SchemaVersion)
	}
	if got.Status != "ok" || !got.Valid {
		t.Fatalf("expected ok valid result, got status=%q valid=%v", got.Status, got.Valid)
	}
	if len(got.Warnings) != 0 || len(got.Errors) != 0 {
		t.Fatalf("expected no warnings/errors, got warnings=%#v errors=%#v", got.Warnings, got.Errors)
	}
	if got.Mode != "proxy-only" || got.Backend != "xray" {
		t.Fatalf("expected proxy-only xray result, got mode=%q backend=%q", got.Mode, got.Backend)
	}
	if got.Profile.ID != p.ID || got.Profile.Name != p.Name || got.Profile.Protocol != p.Protocol {
		t.Fatalf("expected non-sensitive profile metadata to remain stable, got %#v", got.Profile)
	}
	assertNoRawValue(t, out.String(), p.UserIdentity)
}

func TestRunCLIProfileValidateJSONReturnsDiagnosticExitCodeForUnsupportedBackendProfile(t *testing.T) {
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
	err := runWithOptions(context.Background(), []string{"profile", "validate", p.ID, "--mode", "tun", "--json"}, &out, options{profileStorePath: storePath})
	if err == nil {
		t.Fatal("expected profile validate to return diagnostic exit code")
	}
	if got := ExitCode(err); got != 3 {
		t.Fatalf("expected diagnostic exit code 3, got %d", got)
	}

	got := decodeProfileValidateJSON(t, out.Bytes())
	if got.SchemaVersion != "v1" {
		t.Fatalf("expected schema_version v1, got %q", got.SchemaVersion)
	}
	if got.Status != "fail" || got.Valid {
		t.Fatalf("expected fail invalid result, got status=%q valid=%v", got.Status, got.Valid)
	}
	if got.Mode != "tun" || got.Backend != "xray" {
		t.Fatalf("expected tun xray result, got mode=%q backend=%q", got.Mode, got.Backend)
	}
	if len(got.Warnings) != 0 || len(got.Errors) != 1 {
		t.Fatalf("expected exactly one validation error and no warnings, got warnings=%#v errors=%#v", got.Warnings, got.Errors)
	}
	if !strings.Contains(got.Errors[0], "VLESS profiles only") {
		t.Fatalf("expected VLESS-only validation error, got %#v", got.Errors)
	}
	assertNoRawValue(t, out.String(), p.UserIdentity)
}

func TestRunCLIProfileValidateHumanOutputRedactsSensitiveFields(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	store := mustProfileStore(t, storePath)
	p := renderableVLESSProfile()
	if err := store.Add(p); err != nil {
		t.Fatalf("add profile: %v", err)
	}

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"profile", "validate", p.ID, "--mode", "proxy-only"}, &out, options{profileStorePath: storePath})
	if err != nil {
		t.Fatalf("profile validate failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{"Profile validation", "Mode: proxy-only", "Backend: xray", "Status: valid"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected human output to contain %q, got %q", want, got)
		}
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
	if err == nil {
		t.Fatal("expected missing profile validation to fail")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("expected missing profile exit code 1, got %d", got)
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
