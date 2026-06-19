package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLISubscriptionDeleteReportsMatchingManualProfilesLeftUntouched(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	fixturePath := filepath.Join(dir, "diag.txt")
	writeSubscriptionFixture(t, fixturePath, []string{
		shareLink(701, "matching-delete.example", "443", "?type=tcp&security=tls", "subscription-owned"),
	})

	if err := runWithOptions(context.Background(), []string{"subscription", "add", "--name", "diag", "--url", localFileURL(fixturePath)}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription add failed: %v", err)
	}
	if err := runWithOptions(context.Background(), []string{"subscription", "update", "diag"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription update failed: %v", err)
	}
	if err := runWithOptions(context.Background(), []string{"profile", "add", "--name", "manual-match", "--server", "matching-delete.example", "--port", "443", "--protocol", "vless"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("manual profile add failed: %v", err)
	}

	var deleteOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "delete", "diag", "--yes"}, &deleteOut, opts); err != nil {
		t.Fatalf("subscription delete failed: %v", err)
	}
	for _, want := range []string{
		"Subscription deleted: diag",
		"Profiles removed: 1",
		"Profiles with matching servers were left untouched: 1",
	} {
		if !strings.Contains(deleteOut.String(), want) {
			t.Fatalf("expected delete output to contain %q, got %q", want, deleteOut.String())
		}
	}

	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	got := profiles.String()
	if !strings.Contains(got, "manual-match") || !strings.Contains(got, "matching-delete.example") {
		t.Fatalf("expected matching manual profile to remain, got %q", got)
	}
	if strings.Contains(got, "subscription-owned") {
		t.Fatalf("expected subscription-owned profile to be removed, got %q", got)
	}
}
