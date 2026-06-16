package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunImportSendsClientHeaderForHTTPSubscription(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	opts := options{
		profileStorePath:      filepath.Join(dir, "profiles.json"),
		subscriptionStorePath: filepath.Join(dir, "subscriptions.json"),
	}

	var seenClientHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.UserAgent(); got != "TunWarden" {
			t.Fatalf("expected User-Agent %q, got %q", "TunWarden", got)
		}
		w.Header().Set("Content-Type", "application/json")
		seenClientHeader = r.Header.Get("x-hwid")
		if seenClientHeader == "" {
			_, _ = w.Write([]byte(cliUnsupportedClientXrayConfigObject()))
			return
		}
		if strings.Contains(r.URL.RawQuery, seenClientHeader) {
			t.Fatalf("generated identity leaked into URL query: %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(cliRemoteXrayConfigObject("00000000-0000-0000-0000-000000000201", "import-client.example", "import-client")))
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"import", server.URL + "/subscription?token=secret"}, &out, opts); err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if seenClientHeader == "" {
		t.Fatal("expected subscription client header")
	}
	got := out.String()
	for _, want := range []string{"Subscription imported:", "Format: xray-json", "Imported: 1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected import output to contain %q, got %q", want, got)
		}
	}
}

func cliUnsupportedClientXrayConfigObject() string {
	return strings.Replace(
		cliRemoteXrayConfigObject("00000000-0000-0000-0000-000000000202", "dummy-unsupported.example", "dummy-unsupported"),
		"{",
		`{"remarks":"App not supported",`,
		1,
	)
}

func cliRemoteXrayConfigObject(userID, host, tag string) string {
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
        "network": "tcp",
        "security": "tls",
        "tlsSettings": {
          "serverName": %q
        }
      }
    }
  ]
}`, tag, host, userID, host)
}
