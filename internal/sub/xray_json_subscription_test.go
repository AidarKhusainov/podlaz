package sub

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/profile"
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

func TestParseSubscriptionContentImportsXrayJSONObject(t *testing.T) {
	format, parsed, err := ParseSubscriptionContent([]byte(remoteXrayConfigObject("00000000-0000-0000-0000-000000000102", "json-object.example", "json-object", "tcp", "tls")))
	if err != nil {
		t.Fatalf("ParseSubscriptionContent failed: %v", err)
	}
	if format != FormatXrayJSON {
		t.Fatalf("expected format %q, got %q", FormatXrayJSON, format)
	}
	if got := len(parsed.Profiles); got != 1 {
		t.Fatalf("expected 1 profile, got %d", got)
	}
	p := parsed.Profiles[0]
	if p.Source != profile.SourceSubscription {
		t.Fatalf("expected subscription profile source, got %q", p.Source)
	}
	if p.Server != "json-object.example" || p.Transport != "tcp" || p.Security != "tls" {
		t.Fatalf("unexpected normalized profile: %#v", p)
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

func TestParseXrayJSONSubscriptionReportsUnsupportedOutboundsClearly(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unsupported protocol",
			body: `{"outbounds":[{"protocol":"vmess"}]}`,
			want: `unsupported outbound protocol "vmess"`,
		},
		{
			name: "unsupported transport",
			body: remoteXrayConfigObject("00000000-0000-0000-0000-000000000105", "bad-transport.example", "bad-transport", "splithttp", "tls"),
			want: `unsupported VLESS transport "splithttp"`,
		},
		{
			name: "unsupported security",
			body: remoteXrayConfigObject("00000000-0000-0000-0000-000000000106", "bad-security.example", "bad-security", "tcp", "xtls"),
			want: `unsupported VLESS security "xtls"`,
		},
		{
			name: "unsupported transport security combination",
			body: remoteXrayConfigObject("00000000-0000-0000-0000-000000000107", "bad-combo.example", "bad-combo", "ws", "reality"),
			want: "transport/security combination",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := ParseSubscriptionContent([]byte(tt.body))
			if err == nil {
				t.Fatal("expected unsupported outbound to fail")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
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
        }
      }
    }
  ]
}`, tag, host, userID, network, security, host, host)
}
