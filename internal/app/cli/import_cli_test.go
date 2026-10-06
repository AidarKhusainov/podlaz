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
	got := out.String()
	for _, want := range []string{"Imported 1 profile", "Profile: top-level", "Next: podlaz connect"} {
		if !strings.Contains(got, want) {
			t.Fatalf("import output missing %q: %q", want, got)
		}
	}
	for _, secret := range []string{"00000000-0000-0000-0000-000000000001", "example.com"} {
		if strings.Contains(got, secret) {
			t.Fatalf("top-level import leaked %q: %q", secret, got)
		}
	}

	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	if got := profiles.String(); !strings.Contains(got, "top-level") || strings.Contains(got, "example.com") {
		t.Fatalf("unexpected profile list: %q", got)
	}
}

func TestRunCLIImportBase64Subscription(t *testing.T) {
	dir := t.TempDir()
	profileStorePath := filepath.Join(dir, "profiles.json")
	fixturePath := filepath.Join(dir, "sub.txt")
	writeSubscriptionFixture(t, fixturePath, []string{
		shareLink(1, "one.example", "443", "?type=tcp&security=tls&encryption=none", "one"),
		"unsupported://unsupported",
		shareLink(2, "two.example", "8443", "?type=grpc&security=tls&serviceName=svc", "two"),
	})
	sourceURL := localFileURL(fixturePath)
	opts := options{profileStorePath: profileStorePath}

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"import", sourceURL}, &out, opts); err != nil {
		t.Fatalf("top-level subscription import failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{"Subscription imported", "Name: sub.txt", "Profiles: 2", "Skipped unsupported entries: 1", "Next: podlaz profile use <profile>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("import output missing %q: %q", want, got)
		}
	}
	for _, secret := range []string{sourceURL, uuidForTest(1), uuidForTest(2), "one.example", "two.example"} {
		if strings.Contains(got, secret) {
			t.Fatalf("subscription import leaked %q: %q", secret, got)
		}
	}

	var subscriptions bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "list"}, &subscriptions, opts); err != nil {
		t.Fatalf("subscription list failed: %v", err)
	}
	if got := subscriptions.String(); !strings.Contains(got, "sub.txt") || !strings.Contains(got, "base64") {
		t.Fatalf("imported subscription not listed: %q", got)
	}

	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	for _, want := range []string{"one", "two"} {
		if !strings.Contains(profiles.String(), want) {
			t.Fatalf("profile list missing %q: %q", want, profiles.String())
		}
	}
	for _, endpoint := range []string{"one.example", "two.example"} {
		if strings.Contains(profiles.String(), endpoint) {
			t.Fatalf("profile list leaked endpoint %q: %q", endpoint, profiles.String())
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
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("expected injected import failure, got err=%v exit=%d", err, ExitCode(err))
	}

	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	if strings.Contains(profiles.String(), "rollback") {
		t.Fatalf("failed import left profile behind: %q", profiles.String())
	}

	var subscriptions bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "list"}, &subscriptions, opts); err != nil {
		t.Fatalf("subscription list failed: %v", err)
	}
	if strings.Contains(subscriptions.String(), "sub.txt") {
		t.Fatalf("failed import left subscription behind: %q", subscriptions.String())
	}
}

func TestRunCLIImportMalformedTargetDoesNotLeakInput(t *testing.T) {
	secretToken := "00000000-0000-0000-0000-000000000001"
	secretTarget := "https://sub.example.invalid/sub3cr1pt1on3/%zz-" + secretToken

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"import", secretTarget}, &out, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("malformed import err=%v exit=%d", err, ExitCode(err))
	}
	if got := err.Error(); got != "invalid import target: malformed URI or URL" {
		t.Fatalf("unexpected sanitized error: %q", got)
	}
	for _, leaked := range []string{secretTarget, secretToken, "sub3cr1pt1on3"} {
		if strings.Contains(err.Error(), leaked) || strings.Contains(out.String(), leaked) {
			t.Fatalf("malformed import leaked %q", leaked)
		}
	}
}

func TestRunCLIImportRejectsRemovedJSONFlag(t *testing.T) {
	err := runWithOptions(context.Background(), []string{"import", "--json", "vless://demo@example.com:443#demo"}, &bytes.Buffer{}, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
}
