package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportVLESSURIValidRealityFixture(t *testing.T) {
	uri := readFixture(t, "vless-valid-reality.txt")

	p, warnings, err := ImportVLESSURI(uri)
	if err != nil {
		t.Fatalf("import VLESS URI: %v", err)
	}
	if p.ID != "my-vless-profile" {
		t.Fatalf("expected profile id from display name, got %q", p.ID)
	}
	if p.Name != "my-vless-profile" || p.Source != SourceImportedURI || p.Protocol != "vless" {
		t.Fatalf("unexpected normalized profile metadata: %#v", p)
	}
	if p.Server != "example.com" || p.Port != 443 || p.UserIdentity != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("unexpected VLESS endpoint fields: %#v", p)
	}
	if p.Transport != "tcp" || p.Security != "reality" || p.Encryption != "none" || p.Flow != "xtls-rprx-vision" {
		t.Fatalf("unexpected VLESS protocol fields: %#v", p)
	}
	if p.ServerName != "example.com" || p.Fingerprint != "chrome" || p.RealityPublicKey != "public-key" || p.RealityShortID != "abcd" || p.RealitySpiderX != "/" {
		t.Fatalf("unexpected VLESS metadata fields: %#v", p)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "flow is preserved") {
		t.Fatalf("expected flow warning, got %#v", warnings)
	}
}

func TestImportVLESSURIWarnsAboutUnsupportedOptions(t *testing.T) {
	uri := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=ws&security=tls&encryption=none&ed=2048&foo=bar#with-warnings"

	p, warnings, err := ImportVLESSURI(uri)
	if err != nil {
		t.Fatalf("import VLESS URI: %v", err)
	}
	if p.ID != "with-warnings" {
		t.Fatalf("expected imported profile, got %#v", p)
	}
	want := []string{`unsupported VLESS option "ed" ignored`, `unsupported VLESS option "foo" ignored`}
	if len(warnings) != len(want) {
		t.Fatalf("expected warnings %#v, got %#v", want, warnings)
	}
	for i := range want {
		if warnings[i] != want[i] {
			t.Fatalf("warning %d mismatch: got %q want %q", i, warnings[i], want[i])
		}
	}
}

func TestImportVLESSURIRejectsInvalidFixtures(t *testing.T) {
	tests := []struct {
		name        string
		uri         string
		wantMessage string
	}{
		{name: "missing-port-fixture", uri: readFixture(t, "vless-invalid-missing-port.txt"), wantMessage: "server port is required"},
		{name: "unsupported-scheme", uri: "vmess://example", wantMessage: "unsupported profile import URI scheme"},
		{name: "invalid-transport", uri: "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=ftp#bad", wantMessage: "unsupported VLESS transport"},
		{name: "invalid-user-id", uri: "vless://this-user-id-is-longer-than-thirty-bytes@example.com:443?type=tcp#bad", wantMessage: "user id must be a UUID"},
		{name: "password", uri: "vless://00000000-0000-0000-0000-000000000001:secret@example.com:443?type=tcp#bad", wantMessage: "password component is not supported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ImportVLESSURI(tt.uri)
			if err == nil {
				t.Fatal("expected import to fail")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected error containing %q, got %q", tt.wantMessage, err.Error())
			}
		})
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return strings.TrimSpace(string(data))
}
