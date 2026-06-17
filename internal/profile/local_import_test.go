package profile

import (
	"encoding/base64"
	"strings"
	"testing"
)

const localImportVLESSJSON = `{
  "log": {"loglevel": "warning"},
  "inbounds": [{"protocol": "socks", "listen": "127.0.0.1", "port": 1080}],
  "outbounds": [
    {
      "tag": "json-vless",
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

func TestImportLocalContentXrayJSONVLESS(t *testing.T) {
	result, err := ImportLocalContent([]byte(localImportVLESSJSON))
	if err != nil {
		t.Fatalf("import local Xray JSON: %v", err)
	}
	if result.Format != LocalImportFormatXrayJSON || result.Inspected != 1 || len(result.Profiles) != 1 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	p := result.Profiles[0]
	if p.Source != SourceImportedFile || p.Protocol != "vless" || p.Name != "json-vless" {
		t.Fatalf("unexpected imported profile metadata: %#v", p)
	}
	if !strings.HasPrefix(p.ID, "vless-example.com-443-") {
		t.Fatalf("expected endpoint-based ID independent of Xray tag, got %q", p.ID)
	}
	if p.Server != "example.com" || p.Port != 443 || p.UserIdentity != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("unexpected imported endpoint fields: %#v", p)
	}
	if p.Transport != "tcp" || p.Security != "reality" || p.Encryption != "none" || p.Flow != "xtls-rprx-vision" {
		t.Fatalf("unexpected VLESS fields: %#v", p)
	}
	if p.ServerName != "example.com" || p.Fingerprint != "chrome" || p.RealityPublicKey != "public-key" || p.RealityShortID != "abcd" || p.RealitySpiderX != "/" {
		t.Fatalf("unexpected stream settings fields: %#v", p)
	}
}

func TestImportLocalContentXrayJSONRejectsUnsafeTag(t *testing.T) {
	content := strings.Replace(localImportVLESSJSON, `"tag": "json-vless"`, `"tag": "00000000-0000-0000-0000-000000000999"`, 1)

	result, err := ImportLocalContent([]byte(content))
	if err != nil {
		t.Fatalf("import local Xray JSON with unsafe tag: %v", err)
	}
	if len(result.Profiles) != 1 || result.Profiles[0].Name != "vless-example.com-443" {
		t.Fatalf("expected safe fallback name for unsafe tag, got %#v", result.Profiles)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Entry != 1 || result.Warnings[0].Message != DisplayNameRejectedWarning {
		t.Fatalf("expected redacted display-name rejection warning, got %#v", result.Warnings)
	}
}

func TestImportLocalContentXrayJSONDeduplicatesDisplayNames(t *testing.T) {
	content := strings.Replace(localImportVLESSJSON,
		`{"id": "00000000-0000-0000-0000-000000000001", "encryption": "none", "flow": "xtls-rprx-vision"}`,
		`{"id": "00000000-0000-0000-0000-000000000001", "encryption": "none", "flow": "xtls-rprx-vision"},
              {"id": "00000000-0000-0000-0000-000000000002", "encryption": "none", "flow": "xtls-rprx-vision"}`,
		1)

	result, err := ImportLocalContent([]byte(content))
	if err != nil {
		t.Fatalf("import local Xray JSON with duplicate display names: %v", err)
	}
	assertNames(t, result.Profiles, "json-vless", "json-vless (2)")
}

func TestImportLocalContentXrayJSONIgnoresServiceOutbounds(t *testing.T) {
	content := strings.Replace(localImportVLESSJSON, `"outbounds": [`, `"outbounds": [
    {"protocol":"freedom","tag":"direct"},
    {"protocol":"blackhole","tag":"block"},
    {"protocol":"dns","tag":"dns-out"},
    {"protocol":"loopback","tag":"loopback"},`, 1)

	result, err := ImportLocalContent([]byte(content))
	if err != nil {
		t.Fatalf("import mixed local Xray JSON: %v", err)
	}
	if len(result.Profiles) != 1 || len(result.Unsupported) != 0 {
		t.Fatalf("expected one imported profile and no unsupported service outbounds, got %#v", result)
	}
	if result.Inspected != 5 {
		t.Fatalf("expected all outbounds to be inspected, got %d", result.Inspected)
	}
}

func TestImportLocalContentXrayJSONOnlyServiceOutboundsDoesNotReportUnsupportedProtocol(t *testing.T) {
	_, err := ImportLocalContent([]byte(`{
  "outbounds": [
    {"protocol":"freedom","tag":"direct"},
    {"protocol":"blackhole","tag":"block"},
    {"protocol":"dns","tag":"dns-out"},
    {"protocol":"loopback","tag":"loopback"}
  ]
}`))
	if err == nil {
		t.Fatal("expected service-only Xray JSON import to fail")
	}
	if !strings.Contains(err.Error(), "no supported importable outbounds") {
		t.Fatalf("expected no importable outbounds error, got %v", err)
	}
	if strings.Contains(err.Error(), "unsupported outbound protocol") {
		t.Fatalf("service outbounds should not be reported as unsupported protocols: %v", err)
	}
}

func TestImportLocalContentXrayJSONRejectsUnsupportedTransportSecurity(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantMessage string
	}{
		{
			name:        "unsupported-network",
			content:     strings.Replace(localImportVLESSJSON, `"network": "tcp"`, `"network": "ftp"`, 1),
			wantMessage: "unsupported VLESS transport",
		},
		{
			name:        "unsupported-security",
			content:     strings.Replace(localImportVLESSJSON, `"security": "reality"`, `"security": "xtls"`, 1),
			wantMessage: "unsupported VLESS security",
		},
		{
			name:        "incompatible-reality-ws",
			content:     strings.Replace(localImportVLESSJSON, `"network": "tcp"`, `"network": "ws"`, 1),
			wantMessage: "transport/security combination",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ImportLocalContent([]byte(tt.content))
			if err == nil {
				t.Fatal("expected unsupported Xray JSON import to fail")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected error containing %q, got %v", tt.wantMessage, err)
			}
		})
	}
}

func TestImportLocalContentPlainURIListFallback(t *testing.T) {
	content := "\n" + localImportVLESSURI("plain") + "\nhysteria2://unsupported.example\n"

	result, err := ImportLocalContent([]byte(content))
	if err != nil {
		t.Fatalf("import plain URI-list: %v", err)
	}
	if result.Format != LocalImportFormatURIList || result.Inspected != 2 || len(result.Profiles) != 1 || len(result.Unsupported) != 1 {
		t.Fatalf("unexpected plain URI-list result: %#v", result)
	}
	if result.Profiles[0].Source != SourceImportedFile || result.Profiles[0].Name != "plain" {
		t.Fatalf("expected imported_file source and preserved fragment name, got %#v", result.Profiles[0])
	}
}

func TestImportLocalContentURIListPercentEncodedAndUnicodeNames(t *testing.T) {
	content := localImportVLESSURI("Russia%201") + "\n" + localImportVLESSURIWithID("日本-1", "00000000-0000-0000-0000-000000000002")

	result, err := ImportLocalContent([]byte(content))
	if err != nil {
		t.Fatalf("import URI-list with encoded and Unicode names: %v", err)
	}
	assertNames(t, result.Profiles, "Russia 1", "日本-1")
}

func TestImportLocalContentURIListDeduplicatesDisplayNames(t *testing.T) {
	content := localImportVLESSURIWithID("same", "00000000-0000-0000-0000-000000000001") + "\n" + localImportVLESSURIWithID("same", "00000000-0000-0000-0000-000000000002")

	result, err := ImportLocalContent([]byte(content))
	if err != nil {
		t.Fatalf("import URI-list with duplicate display names: %v", err)
	}
	assertNames(t, result.Profiles, "same", "same (2)")
}

func TestImportLocalContentBase64URIListFallback(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(localImportVLESSURI("encoded") + "\n"))

	result, err := ImportLocalContent([]byte(encoded))
	if err != nil {
		t.Fatalf("import Base64 URI-list: %v", err)
	}
	if result.Format != LocalImportFormatBase64URIList || len(result.Profiles) != 1 || result.Profiles[0].Source != SourceImportedFile || result.Profiles[0].Name != "encoded" {
		t.Fatalf("unexpected Base64 URI-list result: %#v", result)
	}
}

func TestImportLocalContentRejectsMalformedJSONObjectWithoutFallback(t *testing.T) {
	_, err := ImportLocalContent([]byte(`{"outbounds":`))
	if err == nil {
		t.Fatal("expected malformed JSON object to fail")
	}
	if !strings.Contains(err.Error(), "malformed Xray JSON") {
		t.Fatalf("expected malformed Xray JSON error, got %v", err)
	}
}

func TestImportLocalContentRejectsWrongJSONTopLevelTypes(t *testing.T) {
	for _, content := range []string{`[]`, `"vless://00000000-0000-0000-0000-000000000001@example.com:443"`, `1`, `true`, `null`} {
		t.Run(content, func(t *testing.T) {
			_, err := ImportLocalContent([]byte(content))
			if err == nil {
				t.Fatal("expected unsupported JSON top-level type")
			}
			if !strings.Contains(err.Error(), "unsupported local import JSON top-level type") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestImportLocalContentDuplicateURIListFailsAtomicallyBeforePersistence(t *testing.T) {
	uri := localImportVLESSURI("duplicate")
	_, err := ImportLocalContent([]byte(uri + "\n" + uri + "\n"))
	if err == nil {
		t.Fatal("expected duplicate local import to fail")
	}
	if !strings.Contains(err.Error(), "duplicate profile id") {
		t.Fatalf("unexpected duplicate error: %v", err)
	}
}

func TestValidateImportedFileRequiresProtocolIdentity(t *testing.T) {
	p := Profile{
		ID:       "missing-identity",
		Name:     "missing identity",
		Source:   SourceImportedFile,
		Engine:   EngineXray,
		Server:   "example.com",
		Port:     443,
		Protocol: "vless",
	}
	if err := Validate(p); err == nil || !strings.Contains(err.Error(), "user_identity is required for imported VLESS profiles") {
		t.Fatalf("expected imported_file VLESS identity validation, got %v", err)
	}
}

func localImportVLESSURI(name string) string {
	return localImportVLESSURIWithID(name, "00000000-0000-0000-0000-000000000001")
}

func localImportVLESSURIWithID(name, id string) string {
	return "vless://" + id + "@example.com:443?type=tcp&security=tls&encryption=none#" + name
}

func assertNames(t *testing.T, profiles []Profile, names ...string) {
	t.Helper()
	got := make(map[string]int, len(profiles))
	for _, p := range profiles {
		got[p.Name]++
	}
	for _, name := range names {
		if got[name] != 1 {
			t.Fatalf("expected imported display name %q exactly once, got profiles %#v", name, profiles)
		}
	}
	if len(profiles) != len(names) {
		t.Fatalf("expected %d profiles, got %#v", len(names), profiles)
	}
}
