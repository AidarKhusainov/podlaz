package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/sub"
)

func TestRunCLIImportHTTPBase64SubscriptionPersistsFormat(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte(shareLink(20, "http-base64.example", "443", "?type=tcp&security=tls&encryption=none", "http-base64")))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	secretToken := "provider-token-secret"
	sourceURL := server.URL + "/sub?token=" + secretToken
	opts := options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")}

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"import", sourceURL}, &out, opts); err != nil {
		t.Fatalf("HTTP Base64 subscription import failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Subscription imported") || !strings.Contains(got, "Profiles: 1") || strings.Contains(got, secretToken) || strings.Contains(got, uuidForTest(20)) {
		t.Fatalf("unexpected HTTP Base64 import output: %q", got)
	}
	assertPersistedSubscriptionFormat(t, opts, sub.FormatBase64)
}

func TestRunCLIImportHTTPXrayJSONSubscriptionPersistsFormat(t *testing.T) {
	userID := uuidForTest(21)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(cliXrayJSONSubscription(userID, "http-json.example", "http-json", "tcp", "tls")))
	}))
	defer server.Close()

	secretToken := "provider-token-secret"
	sourceURL := server.URL + "/sub?token=" + secretToken
	opts := options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")}

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"import", sourceURL}, &out, opts); err != nil {
		t.Fatalf("HTTP Xray JSON subscription import failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Subscription imported") || strings.Contains(got, secretToken) || strings.Contains(got, userID) {
		t.Fatalf("unexpected HTTP Xray JSON import output: %q", got)
	}
	assertPersistedSubscriptionFormat(t, opts, sub.FormatXrayJSON)
	assertStoredProfileServer(t, opts.profileStorePath, "http-json.example")
}

func TestRunCLISubscriptionUpdateHTTPXrayJSONPreservesLastKnownGood(t *testing.T) {
	body := cliXrayJSONSubscription(uuidForTest(22), "stable-json.example", "stable-json", "tcp", "tls")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	opts := options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")}
	if err := runWithOptions(context.Background(), []string{"import", server.URL + "/sub"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription import failed: %v", err)
	}
	source := onlySubscription(t, opts)

	var updateOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "update", source.ID}, &updateOut, opts); err != nil {
		t.Fatalf("subscription update failed: %v", err)
	}
	if !strings.Contains(updateOut.String(), "Subscription updated") {
		t.Fatalf("unexpected update output: %q", updateOut.String())
	}
	assertPersistedSubscriptionFormat(t, opts, sub.FormatXrayJSON)

	body = " {definitely-not-json"
	err := runWithOptions(context.Background(), []string{"subscription", "update", source.ID}, &bytes.Buffer{}, opts)
	if err == nil {
		t.Fatal("expected malformed JSON update to fail")
	}
	if !strings.Contains(err.Error(), "Xray JSON") || strings.Contains(err.Error(), "Base64") {
		t.Fatalf("expected JSON parse error without Base64 fallback, got %v", err)
	}
	assertStoredProfileServer(t, opts.profileStorePath, "stable-json.example")
}

func TestRunCLIImportAndUpdateFileURLXrayJSONSubscription(t *testing.T) {
	dir := t.TempDir()
	profileStorePath := filepath.Join(dir, "profiles.json")
	fixturePath := filepath.Join(dir, "remote-xray.json")
	writeXrayJSONSubscriptionFixture(t, fixturePath, uuidForTest(26), "stable-file-json.example", "stable-file-json", "tcp", "tls")
	opts := options{profileStorePath: profileStorePath}

	if err := runWithOptions(context.Background(), []string{"import", localFileURL(fixturePath)}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("file Xray JSON subscription import failed: %v", err)
	}
	source := onlySubscription(t, opts)
	if source.Format != sub.FormatXrayJSON {
		t.Fatalf("format=%q want=%q", source.Format, sub.FormatXrayJSON)
	}
	assertStoredProfileServer(t, profileStorePath, "stable-file-json.example")

	if err := os.WriteFile(fixturePath, []byte(" {not-json"), 0o600); err != nil {
		t.Fatalf("write malformed fixture: %v", err)
	}
	if err := runWithOptions(context.Background(), []string{"subscription", "update", source.ID}, &bytes.Buffer{}, opts); err == nil {
		t.Fatal("expected malformed JSON update to fail")
	}
	assertStoredProfileServer(t, profileStorePath, "stable-file-json.example")
}

func assertPersistedSubscriptionFormat(t *testing.T, opts options, want sub.Format) {
	t.Helper()
	source := onlySubscription(t, opts)
	if source.Format != want {
		t.Fatalf("subscription format=%q want=%q", source.Format, want)
	}
}

func onlySubscription(t *testing.T, opts options) sub.Source {
	t.Helper()
	storePath, err := resolvedSubscriptionStorePath(opts)
	if err != nil {
		t.Fatal(err)
	}
	store, err := sub.NewStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 {
		t.Fatalf("subscriptions=%#v", sources)
	}
	return sources[0]
}

func assertStoredProfileServer(t *testing.T, path, want string) {
	t.Helper()
	store, err := profile.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].Server != want {
		t.Fatalf("profiles=%#v, want server %q", profiles, want)
	}
}

func writeXrayJSONSubscriptionFixture(t *testing.T, path, userID, host, tag, network, security string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(cliXrayJSONSubscription(userID, host, tag, network, security)), 0o600); err != nil {
		t.Fatalf("write Xray JSON subscription fixture: %v", err)
	}
}

func cliXrayJSONSubscription(userID, host, tag, network, security string) string {
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
        }
      }
    }
  ]
}`, tag, host, userID, network, security, host, host)
}
