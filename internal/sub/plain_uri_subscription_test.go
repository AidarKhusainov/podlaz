package sub

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemotePlainURIListAndLegacyBase64(t *testing.T) {
	first := workflowShareLink(1, "example.com", "443", "first")
	second := workflowShareLink(2, "example.org", "443", "second")
	for _, tt := range []struct {
		name, body       string
		format           Format
		profiles, issues int
	}{
		{"plain", first + "\r\n" + second + "\n", FormatURIList, 2, 0},
		{"mixed and duplicate", first + "\n" + first + "\nhysteria2://opaque@example.net:443\n" + second, FormatURIList, 2, 2},
		{"base64", base64.RawStdEncoding.EncodeToString([]byte(first + "\n" + second)), FormatBase64, 2, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			format, parsed, err := ParseSubscriptionContent([]byte(tt.body))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if format != tt.format || len(parsed.Profiles) != tt.profiles || len(parsed.Unsupported) != tt.issues {
				t.Fatalf("format=%q profiles=%d issues=%d", format, len(parsed.Profiles), len(parsed.Unsupported))
			}
		})
	}
}

func TestPlainURIListRecognitionDoesNotMaskErrors(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		format     Format
	}{
		{"invalid uri", "vless://invalid-credential@example.net:443", FormatURIList},
		{"unsupported only", "hysteria2://credential@example.net:443", FormatURIList},
		{"broken json", "{\"outbounds\":", FormatXrayJSON},
		{"broken base64", "invalid-base64!", FormatBase64},
	} {
		t.Run(tt.name, func(t *testing.T) {
			format, _, err := ParseSubscriptionContent([]byte(tt.body))
			if format != tt.format || err == nil {
				t.Fatalf("format=%q err=%v", format, err)
			}
			if strings.Contains(err.Error(), "invalid-credential") || strings.Contains(err.Error(), "credential@example.net") {
				t.Fatalf("secret leaked: %v", err)
			}
		})
	}
}

func TestHTTPPlainURIListRefreshPreservesLastGoodState(t *testing.T) {
	body := workflowShareLink(1, "example.com", "443", "last-good")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	profiles, subscriptions := newSourceWorkflowStores(t, t.TempDir())
	original, err := ImportSource(context.Background(), subscriptions, profiles, server.URL, SourceWorkflowOptions{})
	if err != nil {
		t.Fatalf("HTTP import: %v", err)
	}
	if original.Subscription.Format != FormatURIList {
		t.Fatalf("format: %s", original.Subscription.Format)
	}
	body = "vless://invalid-credential@example.net:443"
	_, err = UpdateSource(context.Background(), subscriptions, profiles, original.Subscription.ID, SourceWorkflowOptions{})
	if err == nil {
		t.Fatal("expected malformed refresh to fail")
	}
	if strings.Contains(err.Error(), "invalid-credential") {
		t.Fatalf("secret leaked: %v", err)
	}
	saved, err := subscriptions.Get(original.Subscription.ID)
	if err != nil || saved.Format != FormatURIList || len(saved.ProfileIDs) != 1 {
		t.Fatalf("last-known-good metadata: %+v %v", saved, err)
	}
	stored, err := profiles.List()
	if err != nil || len(stored) != 1 || stored[0].Name != "last-good" {
		t.Fatalf("last-known-good profiles: %+v %v", stored, err)
	}
}

func TestPlainURIListSupportsAllShareProtocols(t *testing.T) {
	vmessJSON := `{"v":"2","ps":"vmess","add":"example.com","port":"443","id":"00000000-0000-0000-0000-000000000002","aid":"0","scy":"auto","net":"tcp","tls":"tls"}`
	links := []string{
		workflowShareLink(1, "example.com", "443", "vless"),
		"vmess://" + base64.RawStdEncoding.EncodeToString([]byte(vmessJSON)),
		"trojan://example-password@example.net:443?type=tcp&security=tls#trojan",
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:example-password")) + "@example.org:8388#shadowsocks",
	}
	format, parsed, err := ParseSubscriptionContent([]byte(strings.Join(links, "\n")))
	if err != nil {
		t.Fatalf("parse mixed share URIs: %v", err)
	}
	if format != FormatURIList || len(parsed.Profiles) != len(links) || len(parsed.Unsupported) != 0 {
		t.Fatalf("format=%q profiles=%d unsupported=%d", format, len(parsed.Profiles), len(parsed.Unsupported))
	}
}
