package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportClashYAMLUsesCanonicalLocalEntryPoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.yaml")
	const fixture = `proxies:
  - name: example-profile
    type: vless
    server: edge.example.com
    port: 443
    uuid: 00000000-0000-0000-0000-000000000001
    tls: true
    servername: edge.example.com
`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	var output bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"import", path}, &output, opts); err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if !strings.Contains(output.String(), "Imported 1 profile") ||
		!strings.Contains(output.String(), "Profile: example-profile") ||
		!strings.Contains(output.String(), "Next: podlaz connect") {
		t.Fatalf("unexpected import output: %s", output.String())
	}
	for _, forbidden := range []string{"edge.example.com", "00000000-0000-0000-0000-000000000001"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatal("import output exposed sensitive provider data")
		}
	}
}

func TestImportMalformedClashYAMLIsAtomicAndRedacted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	const credential = "example-private-credential"
	if err := os.WriteFile(path, []byte("proxies:\n  - type: vless\n    server: edge.example.com\n    port: 443\n    uuid: "+credential+"\n    skip-cert-verify: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	var output bytes.Buffer
	err := runWithOptions(context.Background(), []string{"import", path}, &output, opts)
	if err == nil || !strings.Contains(err.Error(), "Clash/Mihomo") {
		t.Fatalf("expected format-specific error: %v", err)
	}
	if strings.Contains(err.Error(), credential) || strings.Contains(output.String(), credential) {
		t.Fatal("provider secret leaked")
	}
	if _, statErr := os.Stat(opts.profileStorePath); statErr == nil {
		t.Fatal("failed parse created profile store")
	} else if !os.IsNotExist(statErr) {
		t.Fatal(statErr)
	}
}
