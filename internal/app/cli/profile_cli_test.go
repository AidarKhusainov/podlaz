package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIProfileAddListShowAndDelete(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}

	var addOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "add", "--name", "test", "--server", "example.com", "--port", "443", "--protocol", "vless"}, &addOut, opts); err != nil {
		t.Fatalf("profile add failed: %v", err)
	}
	if addOut.String() != "Profile added: test\n" {
		t.Fatalf("unexpected add output: %q", addOut.String())
	}

	var listOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &listOut, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	if got := listOut.String(); !strings.Contains(got, "test") || !strings.Contains(got, "vless") || !strings.Contains(got, "example.com") {
		t.Fatalf("unexpected list output: %q", got)
	}

	var showOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "show", "test"}, &showOut, opts); err != nil {
		t.Fatalf("profile show failed: %v", err)
	}
	if got := showOut.String(); !strings.Contains(got, "ID: test") || !strings.Contains(got, "Source: manual") || !strings.Contains(got, "Port: 443") {
		t.Fatalf("unexpected show output: %q", got)
	}

	var deleteOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "delete", "test", "--yes"}, &deleteOut, opts); err != nil {
		t.Fatalf("profile delete failed: %v", err)
	}
	if deleteOut.String() != "Profile deleted: test\n" {
		t.Fatalf("unexpected delete output: %q", deleteOut.String())
	}

	err := runWithOptions(context.Background(), []string{"profile", "show", "test"}, &bytes.Buffer{}, opts)
	if err == nil {
		t.Fatal("expected deleted profile to be missing")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("expected missing profile exit code 1, got %d", got)
	}
}

func TestRunCLIProfileDeleteRequiresYes(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	addTestProfile(t, opts)

	err := runWithOptions(context.Background(), []string{"profile", "delete", "test"}, &bytes.Buffer{}, opts)
	if err == nil {
		t.Fatal("expected profile delete without --yes to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
}

func TestRunCLIProfileAddInvalidInputReturnsUsageError(t *testing.T) {
	err := runWithOptions(context.Background(), []string{"profile", "add", "--name", "bad", "--server", "bad host", "--port", "0", "--protocol", "ftp"}, &bytes.Buffer{}, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
	if err == nil {
		t.Fatal("expected invalid input to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
	if !strings.Contains(err.Error(), "unsupported protocol") || !strings.Contains(err.Error(), "server must not contain whitespace") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestRunCLIProfileDuplicateAddReturnsRuntimeError(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	addTestProfile(t, opts)

	err := runWithOptions(context.Background(), []string{"profile", "add", "--name", "test", "--server", "example.com", "--port", "443", "--protocol", "vless"}, &bytes.Buffer{}, opts)
	if err == nil {
		t.Fatal("expected duplicate add to fail")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("expected exit code 1, got %d", got)
	}
}

func TestRunCLIProfileShowMissingReturnsRuntimeError(t *testing.T) {
	err := runWithOptions(context.Background(), []string{"profile", "show", "missing"}, &bytes.Buffer{}, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
	if err == nil {
		t.Fatal("expected missing profile to fail")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("expected exit code 1, got %d", got)
	}
}

func TestRunCLIProfileListJSONShape(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	addTestProfile(t, opts)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list", "--json"}, &out, opts); err != nil {
		t.Fatalf("profile list --json failed: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode list JSON: %v", err)
	}
	assertCommonJSON(t, got)
	profiles, ok := got["profiles"].([]any)
	if !ok || len(profiles) != 1 {
		t.Fatalf("expected one JSON profile, got %#v", got["profiles"])
	}
}

func TestRunCLIProfileShowJSONShape(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	addTestProfile(t, opts)

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "show", "test", "--json"}, &out, opts); err != nil {
		t.Fatalf("profile show --json failed: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode show JSON: %v", err)
	}
	assertCommonJSON(t, got)
	profile, ok := got["profile"].(map[string]any)
	if !ok || profile["id"] != "test" {
		t.Fatalf("expected JSON profile test, got %#v", got["profile"])
	}
}

func addTestProfile(t *testing.T, opts options) {
	t.Helper()
	if err := runWithOptions(context.Background(), []string{"profile", "add", "--name", "test", "--server", "example.com", "--port", "443", "--protocol", "vless"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("profile add failed: %v", err)
	}
}

func assertCommonJSON(t *testing.T, got map[string]any) {
	t.Helper()
	if got["schema_version"] != "v1" {
		t.Fatalf("expected schema_version v1, got %#v", got["schema_version"])
	}
	if got["status"] != "ok" {
		t.Fatalf("expected status ok, got %#v", got["status"])
	}
	if warnings, ok := got["warnings"].([]any); !ok || len(warnings) != 0 {
		t.Fatalf("expected empty warnings, got %#v", got["warnings"])
	}
	if errors, ok := got["errors"].([]any); !ok || len(errors) != 0 {
		t.Fatalf("expected empty errors, got %#v", got["errors"])
	}
}
