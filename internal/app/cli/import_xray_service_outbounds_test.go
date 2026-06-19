package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunImportHTTPXraySubscriptionIgnoresServiceOutbounds(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	opts := options{
		profileStorePath:      filepath.Join(dir, "profiles.json"),
		subscriptionStorePath: filepath.Join(dir, "subscriptions.json"),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.UserAgent(); got != "podlaz" {
			t.Fatalf("expected User-Agent %q, got %q", "podlaz", got)
		}
		if got := r.Header.Get("x-hwid"); got == "" {
			t.Fatal("expected x-hwid header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(cliXrayConfigWithServiceOutbounds()))
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"import", server.URL + "/subscription?token=secret"}, &out, opts); err != nil {
		t.Fatalf("import failed: %v", err)
	}

	got := out.String()
	for _, want := range []string{"Subscription imported:", "Format: xray-json", "Imported: 1", "Unsupported: 0", "Warnings: 0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected import output to contain %q, got %q", want, got)
		}
	}
	for _, unwanted := range []string{"Unsupported entries:", "freedom", "blackhole", "dns-out", "loopback"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("expected service outbounds to stay out of user output; found %q in %q", unwanted, got)
		}
	}
}

func cliXrayConfigWithServiceOutbounds() string {
	return strings.Replace(
		cliRemoteXrayConfigObject("00000000-0000-0000-0000-000000000401", "service-outbounds-import.example", "service-outbounds-import"),
		`"outbounds": [`,
		`"outbounds": [
    {"protocol":"freedom","tag":"direct"},
    {"protocol":"blackhole","tag":"block"},
    {"protocol":"dns","tag":"dns-out"},
    {"protocol":"loopback","tag":"loopback"},`,
		1,
	)
}
