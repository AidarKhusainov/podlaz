package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIProfileImportVLESSListAndShow(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	uri := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=reality&encryption=none&flow=xtls-rprx-vision&sni=example.com&fp=chrome&pbk=public-key&sid=abcd&spx=%2F#my-vless-profile"

	var importOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "import", uri}, &importOut, opts); err != nil {
		t.Fatalf("profile import failed: %v", err)
	}
	importOutput := importOut.String()
	if !strings.Contains(importOutput, "Imported profile: my-vless-profile-") {
		t.Fatalf("unexpected import output: %q", importOutput)
	}
	if strings.Contains(importOutput, "Warnings:") || strings.Contains(importOutput, "flow is preserved") {
		t.Fatalf("import output should not warn for flow supported by proxy-only planning: %q", importOutput)
	}
	if strings.Contains(importOutput, "00000000-0000-0000-0000-000000000001") {
		t.Fatalf("import output leaked VLESS user identity: %q", importOutput)
	}

	profileID := strings.TrimSpace(strings.TrimPrefix(strings.Split(importOutput, "\n")[0], "Imported profile: "))
	if !strings.HasPrefix(profileID, "my-vless-profile-") {
		t.Fatalf("expected imported profile ID with stable prefix, got %q", profileID)
	}

	var listOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &listOut, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	if got := listOut.String(); !strings.Contains(got, profileID) || !strings.Contains(got, "vless") || !strings.Contains(got, "example.com") {
		t.Fatalf("unexpected list output: %q", got)
	}

	var showOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "show", profileID}, &showOut, opts); err != nil {
		t.Fatalf("profile show failed: %v", err)
	}
	show := showOut.String()
	for _, want := range []string{"Source: imported_uri", "User identity: 0000…0001", "Security: reality", "Flow: xtls-rprx-vision", "Reality public key: public-key"} {
		if !strings.Contains(show, want) {
			t.Fatalf("expected profile show to contain %q, got %q", want, show)
		}
	}
	if strings.Contains(show, "00000000-0000-0000-0000-000000000001") {
		t.Fatalf("profile show leaked full VLESS user identity: %q", show)
	}
}

func TestRunCLIProfileImportRejectsMalformedVLESS(t *testing.T) {
	tests := []struct {
		name        string
		uri         string
		wantMessage string
	}{
		{name: "missing-port", uri: "vless://00000000-0000-0000-0000-000000000001@example.com?type=tcp#missing-port", wantMessage: "server port is required"},
		{name: "malformed-query", uri: "vless://00000000-0000-0000-0000-000000000001@example.com:443?path=%ZZ#bad-query", wantMessage: "query is not valid percent-encoding"},
		{name: "incompatible-reality-transport", uri: "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=ws&security=reality#bad", wantMessage: "transport/security combination"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runWithOptions(context.Background(), []string{"profile", "import", tt.uri}, &bytes.Buffer{}, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
			if err == nil {
				t.Fatal("expected malformed import to fail")
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

func TestRunCLIProfileImportSameDisplayNameCreatesDistinctProfiles(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	uris := []string{
		"vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=tls#same-name",
		"vless://00000000-0000-0000-0000-000000000002@example.com:443?type=tcp&security=tls#same-name",
	}

	var ids []string
	for _, uri := range uris {
		var out bytes.Buffer
		if err := runWithOptions(context.Background(), []string{"profile", "import", uri}, &out, opts); err != nil {
			t.Fatalf("profile import failed: %v", err)
		}
		ids = append(ids, strings.TrimSpace(strings.TrimPrefix(out.String(), "Imported profile: ")))
	}
	if ids[0] == ids[1] {
		t.Fatalf("expected distinct imported profile IDs, got %q", ids[0])
	}

	var listOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &listOut, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	for _, id := range ids {
		if !strings.Contains(listOut.String(), id) {
			t.Fatalf("expected list output to contain %q, got %q", id, listOut.String())
		}
	}
}

func TestRunCLIProfileImportJSONIsDeferred(t *testing.T) {
	err := runWithOptions(context.Background(), []string{"profile", "import", "--json", "vless://demo@example.com:443#demo"}, &bytes.Buffer{}, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
	if err == nil {
		t.Fatal("expected profile import --json to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
	if !strings.Contains(err.Error(), "profile import --json is not implemented") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCLIProfileShowJSONRedactsImportedUserIdentity(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	uri := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=tls&encryption=none#json-redaction"
	var importOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "import", uri}, &importOut, opts); err != nil {
		t.Fatalf("profile import failed: %v", err)
	}
	profileID := strings.TrimSpace(strings.TrimPrefix(importOut.String(), "Imported profile: "))

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "show", profileID, "--json"}, &out, opts); err != nil {
		t.Fatalf("profile show --json failed: %v", err)
	}
	if strings.Contains(out.String(), "00000000-0000-0000-0000-000000000001") {
		t.Fatalf("profile show --json leaked full VLESS user identity: %q", out.String())
	}
	if !strings.Contains(out.String(), "0000…0001") {
		t.Fatalf("profile show --json did not include redacted identity: %q", out.String())
	}

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode show JSON: %v", err)
	}
	assertCommonJSON(t, got)
}
