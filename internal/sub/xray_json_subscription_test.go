package sub

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestParseSubscriptionContentKeepsBase64SubscriptionBehavior(t *testing.T) {
	content := base64.StdEncoding.EncodeToString([]byte(strings.Join([]string{
		entry("00000000-0000-0000-0000-000000000101", "base64.example", "443", "?type=tcp&security=tls", "base64"),
		unsupportedEntry("hy", "steria"),
	}, "\n")))

	format, parsed, err := ParseSubscriptionContent([]byte(content))
	if err != nil {
		t.Fatalf("ParseSubscriptionContent failed: %v", err)
	}
	if format != FormatBase64 {
		t.Fatalf("expected format %q, got %q", FormatBase64, format)
	}
	if got := len(parsed.Profiles); got != 1 {
		t.Fatalf("expected 1 profile, got %d", got)
	}
	if parsed.Profiles[0].Source != profile.SourceSubscription {
		t.Fatalf("expected subscription profile source, got %q", parsed.Profiles[0].Source)
	}
	if got := len(parsed.Unsupported); got != 1 {
		t.Fatalf("expected 1 unsupported entry, got %d", got)
	}
}

func TestParseSubscriptionContentPreservesXrayJSONObjectOpaque(t *testing.T) {
	body := xrayObjectWithTopLevelField(
		remoteXrayConfigObject("00000000-0000-0000-0000-000000000102", "json-object.example", "json-object", "future-transport", "future-security"),
		`"futureTop":{"mode":"next"}`,
	)
	format, parsed, err := ParseSubscriptionContent([]byte(body))
	if err != nil {
		t.Fatalf("ParseSubscriptionContent failed: %v", err)
	}
	if format != FormatXrayJSON || len(parsed.Profiles) != 1 {
		t.Fatalf("unexpected parsed subscription: format=%q parsed=%#v", format, parsed)
	}
	p := parsed.Profiles[0]
	if p.Source != profile.SourceSubscription || p.Protocol != profile.ProtocolXrayJSON {
		t.Fatalf("expected opaque subscription Xray profile, got %#v", p)
	}
	if p.Server != "" || p.Transport != "" || p.Security != "" {
		t.Fatalf("subscription Xray JSON must not be schema-flattened: %#v", p)
	}
	stored := profile.ProviderXrayConfigJSON(p)
	for _, want := range []string{"futureTop", "future-transport", "future-security"} {
		if !strings.Contains(stored, want) {
			t.Fatalf("subscription Xray source lost %q: %s", want, stored)
		}
	}
}

func TestParseSubscriptionContentNativeXrayRemainsRenderableProxyOnly(t *testing.T) {
	format, parsed, err := ParseSubscriptionContent([]byte(remoteXrayConfigObject("00000000-0000-4000-8000-000000000181", "xhttp.edge.invalid", "xhttp-reality", "xhttp", "reality")))
	if err != nil {
		t.Fatalf("ParseSubscriptionContent failed: %v", err)
	}
	if format != FormatXrayJSON || len(parsed.Profiles) != 1 {
		t.Fatalf("unexpected parsed subscription: format=%q parsed=%#v", format, parsed)
	}
	p := parsed.Profiles[0]
	if p.Source != profile.SourceSubscription || p.Protocol != profile.ProtocolXrayJSON {
		t.Fatalf("unexpected native Xray subscription profile: %#v", p)
	}
	if err := engine.ValidateXrayProxyOnlyProfile(p); err != nil {
		t.Fatalf("expected native Xray subscription profile to be proxy-only renderable: %v", err)
	}
	generated, err := engine.GenerateXrayProxyOnlyConfig(p, engine.DefaultXrayProxyOnlyConfigOptions())
	if err != nil {
		t.Fatalf("generate native Xray proxy-only config: %v", err)
	}
	config := string(generated)
	for _, want := range []string{`"network": "xhttp"`, `"xhttpSettings"`, `"path": "/xhttp"`, `"host": "xhttp.edge.invalid"`, `"realitySettings"`} {
		if !strings.Contains(config, want) {
			t.Fatalf("expected generated xhttp config to contain %s, got %s", want, config)
		}
	}
}

func TestParseSubscriptionContentImportsXrayJSONArray(t *testing.T) {
	body := "[" +
		remoteXrayConfigObject("00000000-0000-0000-0000-000000000103", "array-one.example", "array-one", "tcp", "tls") + "," +
		remoteXrayConfigObject("00000000-0000-0000-0000-000000000104", "array-two.example", "array-two", "grpc", "reality") +
		"]"

	format, parsed, err := ParseSubscriptionContent([]byte(body))
	if err != nil {
		t.Fatalf("ParseSubscriptionContent failed: %v", err)
	}
	if format != FormatXrayJSON {
		t.Fatalf("expected format %q, got %q", FormatXrayJSON, format)
	}
	if got := len(parsed.Profiles); got != 2 {
		t.Fatalf("expected 2 profiles, got %d", got)
	}
}

func TestParseSubscriptionContentMalformedJSONDoesNotFallbackToBase64(t *testing.T) {
	format, _, err := ParseSubscriptionContent([]byte("  {not-json"))
	if err == nil {
		t.Fatal("expected malformed JSON to fail")
	}
	if format != FormatXrayJSON {
		t.Fatalf("expected format %q, got %q", FormatXrayJSON, format)
	}
	if !strings.Contains(err.Error(), "Xray JSON") || strings.Contains(err.Error(), "Base64") {
		t.Fatalf("expected JSON-only error without Base64 fallback, got %v", err)
	}
}

func TestParseSubscriptionContentRejectsJSONScalars(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "string", body: `"hello"`, want: "top-level type string"},
		{name: "number", body: `123`, want: "top-level type number"},
		{name: "true", body: `true`, want: "top-level type boolean"},
		{name: "false", body: `false`, want: "top-level type boolean"},
		{name: "null", body: `null`, want: "top-level type null"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format, _, err := ParseSubscriptionContent([]byte(tt.body))
			if err == nil {
				t.Fatal("expected scalar JSON to fail")
			}
			if format != FormatXrayJSON {
				t.Fatalf("expected format %q, got %q", FormatXrayJSON, format)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestParseSubscriptionContentRejectsUnsupportedClientXrayJSON(t *testing.T) {
	body := xrayObjectWithTopLevelField(
		remoteXrayConfigObject("00000000-0000-0000-0000-000000000109", "dummy.example", "dummy", "tcp", "tls"),
		`"remarks":"App not supported"`,
	)

	format, _, err := ParseSubscriptionContent([]byte(body))
	if err == nil {
		t.Fatal("expected unsupported-client Xray JSON to fail")
	}
	if format != FormatXrayJSON {
		t.Fatalf("expected format %q, got %q", FormatXrayJSON, format)
	}
	if !strings.Contains(err.Error(), "unsupported client") {
		t.Fatalf("expected unsupported-client error, got %v", err)
	}
}

func TestParseSubscriptionContentRejectsNestedUnsupportedClientXrayJSONError(t *testing.T) {
	body := xrayObjectWithTopLevelField(
		remoteXrayConfigObject("00000000-0000-0000-0000-000000000110", "dummy-nested.example", "dummy-nested", "tcp", "tls"),
		`"error":{"message":"unsupported client"}`,
	)

	_, _, err := ParseSubscriptionContent([]byte(body))
	if err == nil {
		t.Fatal("expected nested unsupported-client Xray JSON error to fail")
	}
	if !strings.Contains(err.Error(), "unsupported client") {
		t.Fatalf("expected unsupported-client error, got %v", err)
	}
}

func TestParseXrayJSONSubscriptionDoesNotSchemaRejectFutureOutbounds(t *testing.T) {
	for _, body := range []string{
		`{"outbounds":[{"protocol":"future-protocol","futureOutbound":true}]}`,
		remoteXrayConfigObject("00000000-0000-0000-0000-000000000105", "future.example", "future", "future-transport", "future-security"),
	} {
		_, parsed, err := ParseSubscriptionContent([]byte(body))
		if err != nil {
			t.Fatalf("native Xray schema must remain Xray-owned: %v", err)
		}
		if len(parsed.Profiles) != 1 || parsed.Profiles[0].Protocol != profile.ProtocolXrayJSON {
			t.Fatalf("expected one opaque native Xray profile, got %#v", parsed.Profiles)
		}
	}
}

func TestParseXrayJSONArrayRejectsDuplicateProfileIDs(t *testing.T) {
	entry := remoteXrayConfigObject("00000000-0000-0000-0000-000000000108", "duplicate.example", "duplicate", "tcp", "tls")
	_, _, err := ParseSubscriptionContent([]byte("[" + entry + "," + entry + "]"))
	if err == nil {
		t.Fatal("expected duplicate profile ID to fail")
	}
	if !strings.Contains(err.Error(), "duplicate subscription profile id") {
		t.Fatalf("unexpected duplicate error: %v", err)
	}
}

func xrayObjectWithTopLevelField(object, field string) string {
	return strings.Replace(object, "{", "{"+field+",", 1)
}

func remoteXrayConfigObject(userID, host, tag, network, security string) string {
	return fmt.Sprintf(`{
   "outbounds": [
     {
       "protocol": "vless",
       "tag": %q,
       "settings": {
         "vnext": [
           {
             "address": %q,
             "port": 443,
             "users": [
               {
                 "id": %q,
                 "encryption": "none"
               }
             ]
           }
         ]
       },
       "streamSettings": {
         "network": %q,
         "security": %q,
         "tlsSettings": {
           "serverName": %q
         },
         "realitySettings": {
           "serverName": %q,
           "publicKey": "public-key",
           "shortId": "abcd"
         },
         "grpcSettings": {
           "serviceName": "svc"
         },
         "wsSettings": {
           "path": "/ws"
         },
         "xhttpSettings": {
           "path": "/xhttp",
           "host": %q
         }
       }
     }
   ]
 }`, tag, host, userID, network, security, host, host, host)
}
