package cli

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIImportVLESSShareURI(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "profiles.json")
	opts := options{profileStorePath: storePath}
	uri := "vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=tls&encryption=none#top-level"

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"import", uri}, &out, opts); err != nil {
		t.Fatalf("top-level VLESS import failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Imported profile: top-level-") {
		t.Fatalf("unexpected import output: %q", got)
	}
	if strings.Contains(out.String(), "00000000-0000-0000-0000-000000000001") {
		t.Fatalf("top-level import leaked VLESS user identity: %q", out.String())
	}

	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	if got := profiles.String(); !strings.Contains(got, "top-level-") || !strings.Contains(got, "example.com") {
		t.Fatalf("imported profile not listed: %q", got)
	}
}

func TestRunCLIImportBase64Subscription(t *testing.T) {
	dir := t.TempDir()
	profileStorePath := filepath.Join(dir, "profiles.json")
	fixturePath := filepath.Join(dir, "sub.txt")
	writeSubscriptionFixture(t, fixturePath, []string{
		shareLink(1, "one.example", "443", "?type=tcp&security=tls&encryption=none", "one"),
		unsupportedLink("vm", "ess"),
		shareLink(2, "two.example", "8443", "?type=grpc&security=tls&serviceName=svc", "two"),
	})
	sourceURL := localFileURL(fixturePath)
	opts := options{profileStorePath: profileStorePath}

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"import", sourceURL}, &out, opts); err != nil {
		t.Fatalf("top-level subscription import failed: %v", err)
	}
	for _, want := range []string{"Subscription imported: imported-subscription-", "Imported: 2", "Unsupported: 1", "Warnings: 0", "unsupported URI scheme"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("expected import output to contain %q, got %q", want, out.String())
		}
	}
	if strings.Contains(out.String(), sourceURL) || strings.Contains(out.String(), uuidForTest(1)) || strings.Contains(out.String(), uuidForTest(2)) {
		t.Fatalf("top-level subscription import leaked sensitive source or identity: %q", out.String())
	}

	var subscriptions bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "list"}, &subscriptions, opts); err != nil {
		t.Fatalf("subscription list failed: %v", err)
	}
	if got := subscriptions.String(); !strings.Contains(got, "imported-subscription-") || !strings.Contains(got, "base64") {
		t.Fatalf("imported subscription not listed: %q", got)
	}

	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	for _, want := range []string{"one-", "two-", "one.example", "two.example"} {
		if !strings.Contains(profiles.String(), want) {
			t.Fatalf("expected profile list to contain %q, got %q", want, profiles.String())
		}
	}
}

func TestRunCLIImportSubscriptionRollbackPreservesState(t *testing.T) {
	dir := t.TempDir()
	profileStorePath := filepath.Join(dir, "profiles.json")
	fixturePath := filepath.Join(dir, "sub.txt")
	writeSubscriptionFixture(t, fixturePath, []string{shareLink(1, "rollback.example", "443", "?type=tcp&security=tls", "rollback")})
	opts := options{profileStorePath: profileStorePath}

	subscriptionAfterProfileApplyHook = func() error { return fmt.Errorf("injected import metadata failure") }
	defer func() { subscriptionAfterProfileApplyHook = nil }()

	err := runWithOptions(context.Background(), []string{"import", localFileURL(fixturePath)}, &bytes.Buffer{}, opts)
	if err == nil {
		t.Fatal("expected injected import failure")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("expected exit code 1, got %d", got)
	}

	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	if strings.Contains(profiles.String(), "rollback.example") {
		t.Fatalf("failed import left imported profile behind: %q", profiles.String())
	}

	var subscriptions bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "list"}, &subscriptions, opts); err != nil {
		t.Fatalf("subscription list failed: %v", err)
	}
	if strings.Contains(subscriptions.String(), "imported-subscription-") {
		t.Fatalf("failed import left subscription behind: %q", subscriptions.String())
	}
}

func TestRunCLIImportInvalidUsageExitCode(t *testing.T) {
	err := runWithOptions(context.Background(), []string{"import", "--json", "vless://demo@example.com:443#demo"}, &bytes.Buffer{}, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
	if err == nil {
		t.Fatal("expected import --json to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
	if !strings.Contains(err.Error(), "import --json is not implemented") {
		t.Fatalf("unexpected error: %v", err)
	}
}
