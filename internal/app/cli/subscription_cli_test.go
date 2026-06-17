package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLISubscriptionAddListShowUpdateFile(t *testing.T) {
	dir := t.TempDir()
	profileStorePath := filepath.Join(dir, "profiles.json")
	fixturePath := filepath.Join(dir, "sub.txt")
	writeSubscriptionFixture(t, fixturePath, []string{
		shareLink(1, "one.example", "443", "?type=tcp&security=tls&encryption=none&ignored=value", "one"),
		"unsupported://unsupported",
		shareLink(2, "two.example", "8443", "?type=grpc&security=tls&serviceName=svc", "two"),
	})
	sourceURL := localFileURL(fixturePath)
	opts := options{profileStorePath: profileStorePath}

	var addOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "add", "--name", "my sub", "--url", sourceURL}, &addOut, opts); err != nil {
		t.Fatalf("subscription add failed: %v", err)
	}
	if got := addOut.String(); got != "Subscription added: my-sub\nName: my sub\n" {
		t.Fatalf("unexpected add output: %q", got)
	}

	var listJSON bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "list", "--json"}, &listJSON, opts); err != nil {
		t.Fatalf("subscription list json failed: %v", err)
	}
	assertJSONEnvelope(t, listJSON.Bytes())
	if strings.Contains(listJSON.String(), sourceURL) {
		t.Fatalf("subscription list json leaked source URL: %q", listJSON.String())
	}

	var showOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "show", "my-sub"}, &showOut, opts); err != nil {
		t.Fatalf("subscription show failed: %v", err)
	}
	if strings.Contains(showOut.String(), sourceURL) || !strings.Contains(showOut.String(), "URL: REDACTED") {
		t.Fatalf("subscription show did not redact URL: %q", showOut.String())
	}
	if !strings.Contains(showOut.String(), "Name: my sub") {
		t.Fatalf("subscription show did not include display name: %q", showOut.String())
	}

	var updateOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "update", "my-sub"}, &updateOut, opts); err != nil {
		t.Fatalf("subscription update failed: %v", err)
	}
	for _, want := range []string{"Subscription updated: my-sub", "Name: my sub", "Imported: 2", "Unsupported: 1", "Warnings: 1", "unsupported profile import URI scheme", "unsupported VLESS option"} {
		if !strings.Contains(updateOut.String(), want) {
			t.Fatalf("expected update output to contain %q, got %q", want, updateOut.String())
		}
	}
	if strings.Contains(updateOut.String(), uuidForTest(1)) {
		t.Fatalf("subscription update leaked full identity: %q", updateOut.String())
	}

	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	for _, want := range []string{"one", "two", "one.example", "two.example"} {
		if !strings.Contains(profiles.String(), want) {
			t.Fatalf("expected profile list to contain %q, got %q", want, profiles.String())
		}
	}
}

func TestRunCLISubscriptionAddUsesSafeFallbackName(t *testing.T) {
	dir := t.TempDir()
	profileStorePath := filepath.Join(dir, "profiles.json")
	fixturePath := filepath.Join(dir, "fallback-sub.txt")
	writeSubscriptionFixture(t, fixturePath, []string{shareLink(1, "fallback.example", "443", "?type=tcp&security=tls", "fallback")})
	sourceURL := localFileURL(fixturePath) + "?token=do-not-print"
	opts := options{profileStorePath: profileStorePath}

	var addOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "add", "--url", sourceURL}, &addOut, opts); err != nil {
		t.Fatalf("subscription add without name failed: %v", err)
	}
	if got := addOut.String(); got != "Subscription added: fallback-sub.txt\nName: fallback-sub.txt\n" {
		t.Fatalf("unexpected fallback add output: %q", got)
	}
	if strings.Contains(addOut.String(), sourceURL) || strings.Contains(addOut.String(), "do-not-print") {
		t.Fatalf("fallback subscription add leaked source URL data: %q", addOut.String())
	}
}

func TestRunCLISubscriptionUpdateRollbackPreservesLastKnownGood(t *testing.T) {
	dir := t.TempDir()
	profileStorePath := filepath.Join(dir, "profiles.json")
	fixturePath := filepath.Join(dir, "sub.txt")
	writeSubscriptionFixture(t, fixturePath, []string{shareLink(1, "stable.example", "443", "?type=tcp&security=tls", "stable")})
	opts := options{profileStorePath: profileStorePath}

	if err := runWithOptions(context.Background(), []string{"subscription", "add", "--name", "stable", "--url", localFileURL(fixturePath)}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription add failed: %v", err)
	}
	if err := runWithOptions(context.Background(), []string{"subscription", "update", "stable"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription update failed: %v", err)
	}

	writeSubscriptionFixture(t, fixturePath, []string{shareLink(1, "changed.example", "443", "?type=tcp&security=tls", "stable")})
	subscriptionAfterProfileApplyHook = func() error { return fmt.Errorf("injected subscription metadata failure") }
	defer func() { subscriptionAfterProfileApplyHook = nil }()

	err := runWithOptions(context.Background(), []string{"subscription", "update", "stable"}, &bytes.Buffer{}, opts)
	if err == nil {
		t.Fatal("expected injected update failure")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("expected exit code 1, got %d", got)
	}

	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	if !strings.Contains(profiles.String(), "stable.example") || strings.Contains(profiles.String(), "changed.example") {
		t.Fatalf("failed update did not preserve last-known-good profiles: %q", profiles.String())
	}
}

func TestRunCLISubscriptionInvalidUsageExitCode(t *testing.T) {
	err := runWithOptions(context.Background(), []string{"subscription", "update"}, &bytes.Buffer{}, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
	if err == nil {
		t.Fatal("expected invalid usage")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
}

func writeSubscriptionFixture(t *testing.T, path string, entries []string) {
	t.Helper()
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Join(entries, "\n")))
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func localFileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func shareLink(n int, host, port, query, name string) string {
	return "vl" + "ess" + "://" + uuidForTest(n) + "@" + host + ":" + port + query + "#" + name
}

func uuidForTest(n int) string {
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", n)
}

func assertJSONEnvelope(t *testing.T, data []byte) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	assertCommonJSON(t, got)
}
