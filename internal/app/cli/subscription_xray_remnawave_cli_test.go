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

func TestRunCLIImportHTTPXrayJSONSubscriptionShowsRemnawaveDisplayNames(t *testing.T) {
	entries := []string{
		cliXrayJSONSubscriptionWithRemarks(uuidForTest(801), "nyc1.censor-amoroso.com", "USA"),
		cliXrayJSONSubscriptionWithRemarks(uuidForTest(802), "fin1.censor-amoroso.com", "FINLAND"),
		cliXrayJSONSubscriptionWithRemarks(uuidForTest(803), "nld1.censor-amoroso.com", "Netherlands"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[" + strings.Join(entries, ",") + "]"))
	}))
	defer server.Close()

	opts := options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")}
	var importOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"import", server.URL + "/subscription"}, &importOut, opts); err != nil {
		t.Fatalf("Remnawave-style Xray JSON subscription import failed: %v", err)
	}
	if got := importOut.String(); !strings.Contains(got, "Format: xray-json") || !strings.Contains(got, "Imported: 3") {
		t.Fatalf("unexpected import output: %q", got)
	}

	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	got := profiles.String()
	for _, want := range []string{"USA", "FINLAND", "Netherlands"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected profile list to contain Remnawave display name %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "proxy") {
		t.Fatalf("profile list should not expose technical outbound tag as display name: %q", got)
	}
}

func cliXrayJSONSubscriptionWithRemarks(userID, host, remarks string) string {
	return strings.Replace(cliXrayJSONSubscription(userID, host, "proxy", "tcp", "tls"), "{", `{"remarks":`+quoteJSONString(remarks)+`,`, 1)
}

func quoteJSONString(value string) string {
	var b bytes.Buffer
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
