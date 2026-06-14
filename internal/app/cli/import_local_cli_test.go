package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIImportLocalXrayJSON(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "config.json")
	mustWriteLocalImportCLIFixture(t, fixturePath, []byte(localImportCLIXrayJSON()))
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"import", fixturePath}, &out, opts); err != nil {
		t.Fatalf("local Xray JSON import failed: %v", err)
	}
	for _, want := range []string{"Local import completed", "Format: xray-json", "Inspected: 1", "Imported: 1", "Skipped: 0", "json-cli-"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("expected local import output to contain %q, got %q", want, out.String())
		}
	}
	if strings.Contains(out.String(), "00000000-0000-0000-0000-000000000001") {
		t.Fatalf("local import output leaked VLESS user identity: %q", out.String())
	}
	profileID := firstLocalImportCLIProfileID(t, out.String())

	var show bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "show", profileID}, &show, opts); err != nil {
		t.Fatalf("profile show failed: %v", err)
	}
	for _, want := range []string{"Source: imported_file", "Protocol: vless", "Security: reality", "Reality public key: public-key"} {
		if !strings.Contains(show.String(), want) {
			t.Fatalf("expected profile show to contain %q, got %q", want, show.String())
		}
	}
	if strings.Contains(show.String(), "00000000-0000-0000-0000-000000000001") {
		t.Fatalf("profile show leaked full VLESS user identity: %q", show.String())
	}
}

func TestRunCLIImportLocalPlainURIList(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "profiles.txt")
	mustWriteLocalImportCLIFixture(t, fixturePath, []byte("\n"+localImportCLIVLESSURI("plain-cli")+"\nhysteria2://unsupported.example\n"))
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"import", fixturePath}, &out, opts); err != nil {
		t.Fatalf("local URI-list import failed: %v", err)
	}
	for _, want := range []string{"Format: uri-list", "Inspected: 2", "Imported: 1", "Skipped: 1", "unsupported profile import URI scheme"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("expected local URI-list output to contain %q, got %q", want, out.String())
		}
	}
}

func TestRunCLIImportLocalBase64URIList(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "profiles.base64")
	encoded := base64.StdEncoding.EncodeToString([]byte(localImportCLIVLESSURI("base64-cli") + "\n"))
	mustWriteLocalImportCLIFixture(t, fixturePath, []byte(encoded))
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"import", fixturePath}, &out, opts); err != nil {
		t.Fatalf("local Base64 URI-list import failed: %v", err)
	}
	for _, want := range []string{"Format: base64-uri-list", "Imported: 1", "base64-cli-"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("expected local Base64 URI-list output to contain %q, got %q", want, out.String())
		}
	}
}

func TestRunCLIImportLocalMalformedJSONDoesNotFallback(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "broken.json")
	mustWriteLocalImportCLIFixture(t, fixturePath, []byte(`{"outbounds":`))
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}

	err := runWithOptions(context.Background(), []string{"import", fixturePath}, &bytes.Buffer{}, opts)
	if err == nil {
		t.Fatal("expected malformed local JSON import to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
	if !strings.Contains(err.Error(), "malformed Xray JSON") {
		t.Fatalf("expected malformed Xray JSON error, got %v", err)
	}
}

func TestRunCLIImportLocalDuplicateIsAtomic(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "duplicates.txt")
	uri := localImportCLIVLESSURI("atomic-duplicate")
	mustWriteLocalImportCLIFixture(t, fixturePath, []byte(uri+"\n"+uri+"\n"))
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}

	err := runWithOptions(context.Background(), []string{"import", fixturePath}, &bytes.Buffer{}, opts)
	if err == nil {
		t.Fatal("expected duplicate local import to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
	if !strings.Contains(err.Error(), "duplicate profile id") {
		t.Fatalf("unexpected duplicate error: %v", err)
	}

	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	if strings.Contains(profiles.String(), "atomic-duplicate") {
		t.Fatalf("failed duplicate import left partial profile state: %q", profiles.String())
	}
}

func localImportCLIXrayJSON() string {
	return `{
  "outbounds": [
    {
      "tag": "json-cli",
      "protocol": "vless",
      "settings": {
        "vnext": [
          {
            "address": "example.com",
            "port": 443,
            "users": [
              {"id": "00000000-0000-0000-0000-000000000001", "encryption": "none", "flow": "xtls-rprx-vision"}
            ]
          }
        ]
      },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "serverName": "example.com",
          "fingerprint": "chrome",
          "publicKey": "public-key",
          "shortId": "abcd",
          "spiderX": "/"
        }
      }
    }
  ]
}`
}

func localImportCLIVLESSURI(name string) string {
	return "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=tls&encryption=none#" + name
}

func mustWriteLocalImportCLIFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", path, err)
	}
}

func firstLocalImportCLIProfileID(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "- "))
		}
	}
	t.Fatalf("did not find imported profile id in output: %q", output)
	return ""
}
