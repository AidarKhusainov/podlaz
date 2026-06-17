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
	if !strings.HasPrefix(p.ID, "vless-example.com-443-") {
		t.Fatalf("expected stable endpoint-based profile id, got %q", p.ID)
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
	if len(warnings) != 0 {
		t.Fatalf("flow is supported by proxy-only Xray config generation and should not warn, got %#v", warnings)
	}
}

func TestImportVLESSURIPreservesFlowWithoutWarning(t *testing.T) {
	uri := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=tls&encryption=none&flow=xtls-rprx-vision#flow-profile"

	p, warnings, err := ImportVLESSURI(uri)
	if err != nil {
		t.Fatalf("import VLESS URI: %v", err)
	}
	if p.Flow != "xtls-rprx-vision" {
		t.Fatalf("expected flow to be preserved, got %q", p.Flow)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for supported flow, got %#v", warnings)
	}
}

func TestImportVLESSURIWarnsAboutUnsupportedOptions(t *testing.T) {
	uri := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=ws&security=tls&encryption=none&ed=2048&foo=bar#with-warnings"

	p, warnings, err := ImportVLESSURI(uri)
	if err != nil {
		t.Fatalf("import VLESS URI: %v", err)
	}
	if !strings.HasPrefix(p.ID, "vless-example.com-443-") || p.Name != "with-warnings" {
		t.Fatalf("expected endpoint-based ID with provider display name, got %#v", p)
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

func TestImportVLESSURIRejectsUnsafeDisplayName(t *testing.T) {
	uri := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=tls#00000000-0000-0000-0000-000000000999"

	p, warnings, err := ImportVLESSURI(uri)
	if err != nil {
		t.Fatalf("import VLESS URI: %v", err)
	}
	if p.Name != "vless-example.com-443" {
		t.Fatalf("expected safe fallback display name, got %q", p.Name)
	}
	if len(warnings) != 1 || warnings[0] != DisplayNameRejectedWarning {
		t.Fatalf("expected display-name rejection warning, got %#v", warnings)
	}
}

func TestImportVLESSURIRejectsInvalidFixtures(t *testing.T) {
	tests := []struct {
		name        string
		uri         string
		wantMessage string
	}{
		{name: "missing-port-fixture", uri: readFixture(t, "vless-invalid-missing-port.txt"), wantMessage: "server port is required"},
		{name: "malformed-query-percent-encoding", uri: "vless://00000000-0000-0000-0000-000000000001@example.com:443?path=%ZZ#bad-query", wantMessage: "query is not valid percent-encoding"},
		{name: "unsupported-scheme", uri: "vmess://example", wantMessage: "unsupported profile import URI scheme"},
		{name: "invalid-transport", uri: "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=ftp#bad", wantMessage: "unsupported VLESS transport"},
		{name: "incompatible-reality-transport", uri: "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=ws&security=reality#bad", wantMessage: "transport/security combination"},
		{name: "invalid-user-id", uri: "vless://this-user-id-is-longer-than-thirty-bytes@example.com:443?type=tcp#bad", wantMessage: "user id must be a UUID"},
		{name: "password", uri: "vless://00000000-0000-0000-0000-000000000001:ignored@example.com:443?type=tcp#bad", wantMessage: "password component is not supported"},
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

func TestImportVLESSURIRealityTransportCompatibility(t *testing.T) {
	for _, transport := range []string{"tcp", "raw", "xhttp", "grpc"} {
		t.Run(transport, func(t *testing.T) {
			uri := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=" + transport + "&security=reality#ok"
			if _, _, err := ImportVLESSURI(uri); err != nil {
				t.Fatalf("expected %s+reality to be accepted: %v", transport, err)
			}
		})
	}
}

func TestImportVLESSURIDeterministicIDUsesConnectionFingerprint(t *testing.T) {
	firstURI := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=tls#first-name"
	sameConnectionRenamedURI := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=tls#renamed"
	secondURI := "vless://00000000-0000-0000-0000-000000000002@example.com:443?type=tcp&security=tls#first-name"

	first, _, err := ImportVLESSURI(firstURI)
	if err != nil {
		t.Fatalf("import first VLESS URI: %v", err)
	}
	firstRenamed, _, err := ImportVLESSURI(sameConnectionRenamedURI)
	if err != nil {
		t.Fatalf("import renamed VLESS URI: %v", err)
	}
	second, _, err := ImportVLESSURI(secondURI)
	if err != nil {
		t.Fatalf("import second VLESS URI: %v", err)
	}

	if first.ID != firstRenamed.ID {
		t.Fatalf("expected stable ID across display-name changes, got %q and %q", first.ID, firstRenamed.ID)
	}
	if first.ID == second.ID {
		t.Fatalf("expected distinct IDs for different VLESS identities, both got %q", first.ID)
	}
	if first.Name != "first-name" || firstRenamed.Name != "renamed" || second.Name != "first-name" {
		t.Fatalf("expected fragments to remain display names, got %q, %q, %q", first.Name, firstRenamed.Name, second.Name)
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
