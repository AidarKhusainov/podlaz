package cli

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLISubscriptionDeleteRemovesOnlyOwnedProfiles(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	targetFixture := filepath.Join(dir, "target.txt")
	otherFixture := filepath.Join(dir, "other.txt")
	writeSubscriptionFixture(t, targetFixture, []string{
		shareLink(101, "personal-one.example", "443", "?type=tcp&security=tls", "personal-one"),
		shareLink(102, "personal-two.example", "443", "?type=tcp&security=tls", "personal-two"),
	})
	writeSubscriptionFixture(t, otherFixture, []string{
		shareLink(201, "work-one.example", "443", "?type=tcp&security=tls", "work-one"),
	})

	if err := runWithOptions(context.Background(), []string{"subscription", "add", "--name", "personal", "--url", localFileURL(targetFixture) + "?token=secret"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("target subscription add failed: %v", err)
	}
	if err := runWithOptions(context.Background(), []string{"subscription", "update", "personal"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("target subscription update failed: %v", err)
	}
	if err := runWithOptions(context.Background(), []string{"subscription", "add", "--name", "work", "--url", localFileURL(otherFixture)}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("other subscription add failed: %v", err)
	}
	if err := runWithOptions(context.Background(), []string{"subscription", "update", "work"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("other subscription update failed: %v", err)
	}
	if err := runWithOptions(context.Background(), []string{"profile", "add", "--name", "manual", "--server", "manual.example", "--port", "443", "--protocol", "vless"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("manual profile add failed: %v", err)
	}
	if err := runWithOptions(context.Background(), []string{"profile", "import", shareLink(301, "oneoff.example", "443", "?type=tcp&security=tls", "oneoff")}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("one-off profile import failed: %v", err)
	}

	var deleteOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "delete", "personal", "--yes"}, &deleteOut, opts); err != nil {
		t.Fatalf("subscription delete failed: %v", err)
	}
	for _, want := range []string{"Subscription deleted: personal", "Profiles removed: 2"} {
		if !strings.Contains(deleteOut.String(), want) {
			t.Fatalf("expected delete output to contain %q, got %q", want, deleteOut.String())
		}
	}
	if strings.Contains(deleteOut.String(), "Type yes to continue") {
		t.Fatalf("--yes delete unexpectedly prompted: %q", deleteOut.String())
	}
	for _, leaked := range []string{localFileURL(targetFixture), "token=secret", uuidForTest(101), uuidForTest(102)} {
		if strings.Contains(deleteOut.String(), leaked) {
			t.Fatalf("subscription delete leaked sensitive value %q in %q", leaked, deleteOut.String())
		}
	}

	missingErr := runWithOptions(context.Background(), []string{"subscription", "show", "personal"}, &bytes.Buffer{}, opts)
	if missingErr == nil || ExitCode(missingErr) != 1 {
		t.Fatalf("expected deleted subscription lookup to fail with exit code 1, got %v", missingErr)
	}

	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	for _, removed := range []string{"personal-one.example", "personal-two.example"} {
		if strings.Contains(profiles.String(), removed) {
			t.Fatalf("deleted subscription profile %q is still present: %q", removed, profiles.String())
		}
	}
	for _, preserved := range []string{"work-one.example", "manual.example", "oneoff.example"} {
		if !strings.Contains(profiles.String(), preserved) {
			t.Fatalf("expected preserved profile %q, got %q", preserved, profiles.String())
		}
	}
}

func TestRunCLISubscriptionDeleteKeepProfiles(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	fixturePath := filepath.Join(dir, "keep.txt")
	writeSubscriptionFixture(t, fixturePath, []string{shareLink(401, "keep.example", "443", "?type=tcp&security=tls", "keep")})

	if err := runWithOptions(context.Background(), []string{"subscription", "add", "--name", "keep", "--url", localFileURL(fixturePath)}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription add failed: %v", err)
	}
	if err := runWithOptions(context.Background(), []string{"subscription", "update", "keep"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription update failed: %v", err)
	}

	var deleteOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "delete", "keep", "--yes", "--keep-profiles"}, &deleteOut, opts); err != nil {
		t.Fatalf("subscription delete --keep-profiles failed: %v", err)
	}
	if got := deleteOut.String(); got != "Subscription deleted: keep\nProfiles kept: 1\n" {
		t.Fatalf("unexpected keep-profiles output: %q", got)
	}

	missingErr := runWithOptions(context.Background(), []string{"subscription", "show", "keep"}, &bytes.Buffer{}, opts)
	if missingErr == nil || ExitCode(missingErr) != 1 {
		t.Fatalf("expected deleted subscription lookup to fail with exit code 1, got %v", missingErr)
	}

	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	if !strings.Contains(profiles.String(), "keep.example") {
		t.Fatalf("expected keep-profiles to preserve imported profile, got %q", profiles.String())
	}
}

func TestRunCLISubscriptionDeleteInteractiveConfirmationDeletes(t *testing.T) {
	dir := t.TempDir()
	opts := options{
		profileStorePath: filepath.Join(dir, "profiles.json"),
		stdin:            strings.NewReader("yes\n"),
		stdinIsTerminal:  func() bool { return true },
	}
	fixturePath := filepath.Join(dir, "interactive.txt")
	writeSubscriptionFixture(t, fixturePath, []string{shareLink(451, "interactive.example", "443", "?type=tcp&security=tls", "interactive")})

	if err := runWithOptions(context.Background(), []string{"subscription", "add", "--name", "interactive", "--url", localFileURL(fixturePath)}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription add failed: %v", err)
	}
	if err := runWithOptions(context.Background(), []string{"subscription", "update", "interactive"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription update failed: %v", err)
	}

	var deleteOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "delete", "interactive"}, &deleteOut, opts); err != nil {
		t.Fatalf("interactive subscription delete failed: %v", err)
	}
	for _, want := range []string{"Delete subscription interactive and remove 1 imported profiles? Type yes to continue:", "Subscription deleted: interactive", "Profiles removed: 1"} {
		if !strings.Contains(deleteOut.String(), want) {
			t.Fatalf("expected interactive output to contain %q, got %q", want, deleteOut.String())
		}
	}
}

func TestRunCLISubscriptionDeleteInteractiveConfirmationCancelPreservesState(t *testing.T) {
	dir := t.TempDir()
	opts := options{
		profileStorePath: filepath.Join(dir, "profiles.json"),
		stdin:            strings.NewReader("no\n"),
		stdinIsTerminal:  func() bool { return true },
	}
	fixturePath := filepath.Join(dir, "cancel.txt")
	writeSubscriptionFixture(t, fixturePath, []string{shareLink(471, "cancel.example", "443", "?type=tcp&security=tls", "cancel")})

	if err := runWithOptions(context.Background(), []string{"subscription", "add", "--name", "cancel", "--url", localFileURL(fixturePath)}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription add failed: %v", err)
	}
	if err := runWithOptions(context.Background(), []string{"subscription", "update", "cancel"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription update failed: %v", err)
	}

	var deleteOut bytes.Buffer
	err := runWithOptions(context.Background(), []string{"subscription", "delete", "cancel"}, &deleteOut, opts)
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "subscription delete canceled") {
		t.Fatalf("expected interactive cancel with exit code 1, got %v", err)
	}
	if !strings.Contains(deleteOut.String(), "Type yes to continue") {
		t.Fatalf("expected cancellation path to prompt, got %q", deleteOut.String())
	}
	if err := runWithOptions(context.Background(), []string{"subscription", "show", "cancel"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription metadata was not preserved after cancel: %v", err)
	}
	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	if !strings.Contains(profiles.String(), "cancel.example") {
		t.Fatalf("profile was not preserved after cancel: %q", profiles.String())
	}
}

func TestRunCLISubscriptionDeleteRequiresYesAndReportsMissingID(t *testing.T) {
	dir := t.TempDir()
	opts := options{
		profileStorePath: filepath.Join(dir, "profiles.json"),
		stdinIsTerminal:  func() bool { return false },
	}
	fixturePath := filepath.Join(dir, "sub.txt")
	writeSubscriptionFixture(t, fixturePath, []string{shareLink(501, "delete-usage.example", "443", "?type=tcp&security=tls", "usage")})
	if err := runWithOptions(context.Background(), []string{"subscription", "add", "--name", "usage", "--url", localFileURL(fixturePath)}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription add failed: %v", err)
	}

	err := runWithOptions(context.Background(), []string{"subscription", "delete", "usage"}, &bytes.Buffer{}, opts)
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "requires --yes") {
		t.Fatalf("expected missing --yes to fail with exit code 2, got %v", err)
	}

	err = runWithOptions(context.Background(), []string{"subscription", "delete", "usage", "--json", "--yes"}, &bytes.Buffer{}, opts)
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "subscription delete --json is not implemented") {
		t.Fatalf("expected subscription delete --json to fail with exit code 2, got %v", err)
	}

	err = runWithOptions(context.Background(), []string{"subscription", "delete", "missing", "--yes"}, &bytes.Buffer{}, opts)
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "subscription not found") {
		t.Fatalf("expected missing subscription to fail clearly with exit code 1, got %v", err)
	}
}

func TestRunCLISubscriptionDeleteFailurePreservesState(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	fixturePath := filepath.Join(dir, "stable.txt")
	writeSubscriptionFixture(t, fixturePath, []string{shareLink(601, "stable-delete.example", "443", "?type=tcp&security=tls", "stable-delete")})

	if err := runWithOptions(context.Background(), []string{"subscription", "add", "--name", "stable", "--url", localFileURL(fixturePath)}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription add failed: %v", err)
	}
	if err := runWithOptions(context.Background(), []string{"subscription", "update", "stable"}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("subscription update failed: %v", err)
	}

	subscriptionAfterProfileApplyHook = func() error { return fmt.Errorf("injected subscription delete failure") }
	defer func() { subscriptionAfterProfileApplyHook = nil }()

	err := runWithOptions(context.Background(), []string{"subscription", "delete", "stable", "--yes"}, &bytes.Buffer{}, opts)
	if err == nil {
		t.Fatal("expected injected delete failure")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("expected exit code 1, got %d", got)
	}

	var showOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "show", "stable"}, &showOut, opts); err != nil {
		t.Fatalf("subscription metadata was not preserved: %v", err)
	}
	var profiles bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"profile", "list"}, &profiles, opts); err != nil {
		t.Fatalf("profile list failed: %v", err)
	}
	if !strings.Contains(profiles.String(), "stable-delete.example") {
		t.Fatalf("profile cleanup rollback did not preserve subscription profile: %q", profiles.String())
	}
}
