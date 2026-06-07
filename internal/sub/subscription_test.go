package sub

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/profile"
)

func TestParseBase64SubscriptionImportsSupportedEntries(t *testing.T) {
	content := encodeLines([]string{
		entry("00000000-0000-0000-0000-000000000001", "example.com", "443", "?type=tcp&security=tls&foo=bar", "one"),
		unsupportedEntry("tr", "ojan"),
		entry("00000000-0000-0000-0000-000000000002", "example.org", "8443", "?type=grpc&security=tls", "two"),
	})
	parsed, err := ParseBase64Subscription([]byte(content))
	if err != nil {
		t.Fatalf("ParseBase64Subscription failed: %v", err)
	}
	if got := len(parsed.Profiles); got != 2 {
		t.Fatalf("expected 2 profiles, got %d", got)
	}
	for _, p := range parsed.Profiles {
		if p.Source != profile.SourceSubscription {
			t.Fatalf("expected subscription profile source, got %q", p.Source)
		}
		if p.UserIdentity == "" {
			t.Fatalf("expected user identity for %s", p.ID)
		}
	}
	if got := len(parsed.Unsupported); got != 1 {
		t.Fatalf("expected 1 unsupported entry, got %d", got)
	}
	if got := len(parsed.Warnings); got != 1 || !strings.Contains(parsed.Warnings[0].Message, "foo") {
		t.Fatalf("expected option warning, got %#v", parsed.Warnings)
	}
}

func TestParseBase64SubscriptionRejectsUnusableContent(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		want    string
	}{
		{name: "invalid-base64", content: "not base64", want: "parse Base64 subscription"},
		{name: "only-unsupported", content: encodeLines([]string{unsupportedEntry("vm", "ess")}), want: "no supported profiles"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseBase64Subscription([]byte(tt.content))
			if err == nil {
				t.Fatal("expected parse to fail")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestParseBase64SubscriptionDeduplicatesProfiles(t *testing.T) {
	line := entry("00000000-0000-0000-0000-000000000001", "example.com", "443", "?type=tcp&security=tls", "same")
	parsed, err := ParseBase64Subscription([]byte(encodeLines([]string{line, line})))
	if err != nil {
		t.Fatalf("ParseBase64Subscription failed: %v", err)
	}
	if got := len(parsed.Profiles); got != 1 {
		t.Fatalf("expected 1 profile, got %d", got)
	}
	if got := len(parsed.Unsupported); got != 1 || !strings.Contains(parsed.Unsupported[0].Message, "duplicate profile id") {
		t.Fatalf("expected duplicate issue, got %#v", parsed.Unsupported)
	}
}

func TestFetchSourceRejectsCrossOriginRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("cross-origin redirect target should not be requested; got %s", r.URL.String())
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/sub?provider_token=secret", http.StatusFound)
	}))
	defer redirector.Close()

	_, err := FetchSource(context.Background(), Source{ID: "redirect", Name: "redirect", URL: redirector.URL + "/sub?provider_token=secret", Format: FormatBase64})
	if err == nil {
		t.Fatal("expected cross-origin redirect to fail")
	}
	if !strings.Contains(err.Error(), "refusing cross-origin subscription redirect") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func encodeLines(lines []string) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))
}

func entry(userID, host, port, query, name string) string {
	return "vl" + "ess" + "://" + userID + "@" + host + ":" + port + query + "#" + name
}

func unsupportedEntry(a, b string) string {
	return a + b + "://unsupported"
}
