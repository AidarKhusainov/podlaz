package cli

import (
	"bytes"
	"context"
	"encoding/base64"
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
	if !strings.Contains(importOutput, "Imported profile: vless-example.com-443-") || !strings.Contains(importOutput, "Name: my-vless-profile") {
		t.Fatalf("unexpected import output: %q", importOutput)
	}
	if strings.Contains(importOutput, "Warnings:") || strings.Contains(importOutput, "flow is preserved") {
		t.Fatalf("import output should not warn for flow supported by proxy-only planning: %q", importOutput)
	}
	if strings.Contains(importOutput, "00000000-0000-0000-0000-000000000001") {
		t.Fatalf("import output leaked VLESS user identity: %q", importOutput)
	}

	profileID := importedProfileIDFromOutput(t, importOutput)
	if !strings.HasPrefix(profileID, "vless-example.com-443-") {
		t.Fatalf("expected imported profile ID with stable endpoint prefix, got %q", profileID)
	}

	var listOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &listOut, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	if got := listOut.String(); !strings.Contains(got, profileID) || !strings.Contains(got, "my-vless-profile") || !strings.Contains(got, "vless") || !strings.Contains(got, "example.com") {
		t.Fatalf("unexpected list output: %q", got)
	}

	var showOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "show", profileID}, &showOut, opts); err != nil {
		t.Fatalf("profile show failed: %v", err)
	}
	show := showOut.String()
	for _, want := range []string{"Name: my-vless-profile", "Source: imported_uri", "User identity: 0000…0001", "Security: reality", "Flow: xtls-rprx-vision", "Reality public key: public-key"} {
		if !strings.Contains(show, want) {
			t.Fatalf("expected profile show to contain %q, got %q", want, show)
		}
	}
	if strings.Contains(show, "00000000-0000-0000-0000-000000000001") {
		t.Fatalf("profile show leaked full VLESS user identity: %q", show)
	}
}

func TestRunCLIProfileImportNewShareURIsListShowAndRedact(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	secret := "secret-password"
	uris := []struct {
		name           string
		uri            string
		profilePrefix  string
		displayName    string
		protocol       string
		showContains   []string
		showNotContain []string
	}{
		{
			name:          "vmess",
			uri:           vmessURIForCLITest(),
			profilePrefix: "vmess-example.com-443-",
			displayName:   "cli-vmess",
			protocol:      "vmess",
			showContains: []string{
				"Protocol: vmess",
				"User identity: 0000…0002",
				"Transport: ws",
				"Security: tls",
			},
			showNotContain: []string{"00000000-0000-0000-0000-000000000002"},
		},
		{
			name:          "trojan",
			uri:           "trojan://" + secret + "@example.com:443?type=grpc&security=tls&sni=example.com&serviceName=svc#cli-trojan",
			profilePrefix: "trojan-example.com-443-",
			displayName:   "cli-trojan",
			protocol:      "trojan",
			showContains: []string{
				"Protocol: trojan",
				"User identity: REDACTED",
				"Transport: grpc",
				"Service name: svc",
			},
			showNotContain: []string{secret},
		},
		{
			name:          "shadowsocks",
			uri:           "ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:"+secret)) + "@example.com:8388#cli-ss",
			profilePrefix: "shadowsocks-example.com-8388-",
			displayName:   "cli-ss",
			protocol:      "shadowsocks",
			showContains: []string{
				"Protocol: shadowsocks",
				"User identity: REDACTED",
				"Encryption: aes-256-gcm",
			},
			showNotContain: []string{secret, "aes-256-gcm:" + secret},
		},
	}

	for _, tt := range uris {
		t.Run(tt.name, func(t *testing.T) {
			var importOut bytes.Buffer
			if err := runWithOptions(context.Background(), []string{"profile", "import", tt.uri}, &importOut, opts); err != nil {
				t.Fatalf("profile import failed: %v", err)
			}
			profileID := importedProfileIDFromOutput(t, importOut.String())
			if !strings.HasPrefix(profileID, tt.profilePrefix) || !strings.Contains(importOut.String(), "Name: "+tt.displayName) {
				t.Fatalf("expected imported profile ID prefix %q and display name %q, got id=%q output=%q", tt.profilePrefix, tt.displayName, profileID, importOut.String())
			}

			var listOut bytes.Buffer
			if err := runWithOptions(context.Background(), []string{"profile", "list"}, &listOut, opts); err != nil {
				t.Fatalf("profile list failed: %v", err)
			}
			if got := listOut.String(); !strings.Contains(got, profileID) || !strings.Contains(got, tt.displayName) || !strings.Contains(got, tt.protocol) || !strings.Contains(got, "example.com") {
				t.Fatalf("unexpected profile list output: %q", got)
			}

			var showOut bytes.Buffer
			if err := runWithOptions(context.Background(), []string{"profile", "show", profileID}, &showOut, opts); err != nil {
				t.Fatalf("profile show failed: %v", err)
			}
			show := showOut.String()
			for _, want := range append([]string{"Name: " + tt.displayName}, tt.showContains...) {
				if !strings.Contains(show, want) {
					t.Fatalf("expected profile show to contain %q, got %q", want, show)
				}
			}
			for _, leaked := range tt.showNotContain {
				if strings.Contains(show, leaked) || strings.Contains(listOut.String(), leaked) || strings.Contains(importOut.String(), leaked) {
					t.Fatalf("CLI output leaked %q\nimport=%q\nlist=%q\nshow=%q", leaked, importOut.String(), listOut.String(), show)
				}
			}

			var jsonOut bytes.Buffer
			if err := runWithOptions(context.Background(), []string{"profile", "show", profileID, "--json"}, &jsonOut, opts); err != nil {
				t.Fatalf("profile show --json failed: %v", err)
			}
			for _, leaked := range tt.showNotContain {
				if strings.Contains(jsonOut.String(), leaked) {
					t.Fatalf("profile show --json leaked %q in %q", leaked, jsonOut.String())
				}
			}
			assertJSONEnvelope(t, jsonOut.Bytes())
		})
	}
}

func vmessURIForCLITest() string {
	payload := `{"v":"2","ps":"cli-vmess","add":"example.com","port":"443","id":"00000000-0000-0000-0000-000000000002","aid":"0","scy":"auto","net":"ws","type":"none","host":"cdn.example.com","path":"/ws","tls":"tls","sni":"example.com"}`
	return "vmess://" + base64.RawStdEncoding.EncodeToString([]byte(payload))
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
		ids = append(ids, importedProfileIDFromOutput(t, out.String()))
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
	profileID := importedProfileIDFromOutput(t, importOut.String())

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

func importedProfileIDFromOutput(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Imported profile: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Imported profile: "))
		}
	}
	t.Fatalf("did not find imported profile ID in output: %q", output)
	return ""
}
