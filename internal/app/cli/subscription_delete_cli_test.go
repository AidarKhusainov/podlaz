package cli

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/sub"
)

func TestRunCLISubscriptionDeleteRemovesOnlyOwnedProfiles(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	target := importDeleteSubscription(t, opts, filepath.Join(dir, "target.txt"), []string{
		shareLink(101, "personal-one.example", "443", "?type=tcp&security=tls", "personal-one"),
		shareLink(102, "personal-two.example", "443", "?type=tcp&security=tls", "personal-two"),
	})
	other := importDeleteSubscription(t, opts, filepath.Join(dir, "other.txt"), []string{
		shareLink(201, "work-one.example", "443", "?type=tcp&security=tls", "work-one"),
	})
	if target.ID == other.ID {
		t.Fatal("fixtures produced duplicate subscription IDs")
	}

	profileStore, _ := profile.NewStore(opts.profileStorePath)
	manual := testConnectProfile()
	manual.ID, manual.Name, manual.Server, manual.Source = "manual-profile", "manual", "manual.example", profile.SourceManual
	if err := profileStore.Add(manual); err != nil {
		t.Fatal(err)
	}
	oneoff := testConnectProfile()
	oneoff.ID, oneoff.Name, oneoff.Server, oneoff.Source = "oneoff-profile", "oneoff", "oneoff.example", profile.SourceImportedURI
	if err := profileStore.Add(oneoff); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "delete", target.ID, "--yes"}, &out, opts); err != nil {
		t.Fatalf("subscription delete failed: %v", err)
	}
	for _, want := range []string{"Subscription deleted: " + target.ID, "Profiles removed: 2"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("delete output missing %q: %q", want, out.String())
		}
	}
	if strings.Contains(out.String(), "token=") {
		t.Fatalf("delete output leaked source material: %q", out.String())
	}

	if _, err := onlySubscriptionByID(opts, target.ID); err == nil {
		t.Fatal("deleted subscription still exists")
	}
	profiles, err := profileStore.List()
	if err != nil {
		t.Fatal(err)
	}
	names := profileNames(profiles)
	for _, removed := range []string{"personal-one", "personal-two"} {
		if names[removed] {
			t.Fatalf("deleted subscription profile %q remains: %#v", removed, profiles)
		}
	}
	for _, preserved := range []string{"work-one", "manual", "oneoff"} {
		if !names[preserved] {
			t.Fatalf("expected preserved profile %q: %#v", preserved, profiles)
		}
	}
}

func TestRunCLISubscriptionDeleteKeepProfiles(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	source := importDeleteSubscription(t, opts, filepath.Join(dir, "keep.txt"), []string{
		shareLink(401, "keep.example", "443", "?type=tcp&security=tls", "keep"),
	})

	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "delete", source.ID, "--yes", "--keep-profiles"}, &out, opts); err != nil {
		t.Fatalf("delete --keep-profiles failed: %v", err)
	}
	if got := out.String(); got != "Subscription deleted: "+source.ID+"\nProfiles kept: 1\n" {
		t.Fatalf("keep output=%q", got)
	}
	store, _ := profile.NewStore(opts.profileStorePath)
	profiles, err := store.List()
	if err != nil || len(profiles) != 1 || profiles[0].Name != "keep" {
		t.Fatalf("kept profiles=%#v err=%v", profiles, err)
	}
}

func TestRunCLISubscriptionDeleteInteractiveDefaultsNo(t *testing.T) {
	dir := t.TempDir()
	opts := options{
		profileStorePath: filepath.Join(dir, "profiles.json"),
		stdin:            strings.NewReader("\n"),
		stdinIsTerminal:  func() bool { return true },
	}
	source := importDeleteSubscription(t, opts, filepath.Join(dir, "cancel.txt"), []string{
		shareLink(471, "cancel.example", "443", "?type=tcp&security=tls", "cancel"),
	})

	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"subscription", "delete", source.ID}, &out, opts)
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "subscription delete canceled") {
		t.Fatalf("empty confirmation err=%v exit=%d", err, ExitCode(err))
	}
	if !strings.Contains(out.String(), "[y/N]:") {
		t.Fatalf("default-no prompt missing: %q", out.String())
	}
	if _, err := onlySubscriptionByID(opts, source.ID); err != nil {
		t.Fatalf("cancel removed subscription: %v", err)
	}
}

func TestRunCLISubscriptionDeleteInteractiveExplicitYesDeletes(t *testing.T) {
	dir := t.TempDir()
	opts := options{
		profileStorePath: filepath.Join(dir, "profiles.json"),
		stdin:            strings.NewReader("yes\n"),
		stdinIsTerminal:  func() bool { return true },
	}
	source := importDeleteSubscription(t, opts, filepath.Join(dir, "interactive.txt"), []string{
		shareLink(451, "interactive.example", "443", "?type=tcp&security=tls", "interactive"),
	})
	var out bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "delete", source.ID}, &out, opts); err != nil {
		t.Fatalf("explicit yes delete failed: %v", err)
	}
	if !strings.Contains(out.String(), "Subscription deleted: "+source.ID) {
		t.Fatalf("delete output=%q", out.String())
	}
}

func TestRunCLISubscriptionDeleteRequiresYesAndReportsMissingID(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json"), stdinIsTerminal: func() bool { return false }}
	source := importDeleteSubscription(t, opts, filepath.Join(dir, "usage.txt"), []string{
		shareLink(501, "delete-usage.example", "443", "?type=tcp&security=tls", "usage"),
	})

	err := runWithOptions(context.Background(), []string{"subscription", "delete", source.ID}, &bytes.Buffer{}, opts)
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "requires --yes") {
		t.Fatalf("missing --yes err=%v exit=%d", err, ExitCode(err))
	}
	err = runWithOptions(context.Background(), []string{"subscription", "delete", source.ID, "--json", "--yes"}, &bytes.Buffer{}, opts)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("obsolete JSON accepted: %v", err)
	}
	err = runWithOptions(context.Background(), []string{"subscription", "delete", "missing", "--yes"}, &bytes.Buffer{}, opts)
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "subscription not found") {
		t.Fatalf("missing subscription err=%v exit=%d", err, ExitCode(err))
	}
}

func TestRunCLISubscriptionDeleteFailurePreservesState(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	source := importDeleteSubscription(t, opts, filepath.Join(dir, "stable.txt"), []string{
		shareLink(601, "stable-delete.example", "443", "?type=tcp&security=tls", "stable-delete"),
	})
	subscriptionAfterProfileApplyHook = func() error { return fmt.Errorf("injected subscription delete failure") }
	defer func() { subscriptionAfterProfileApplyHook = nil }()

	err := runWithOptions(context.Background(), []string{"subscription", "delete", source.ID, "--yes"}, &bytes.Buffer{}, opts)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("injected delete err=%v exit=%d", err, ExitCode(err))
	}
	if _, err := onlySubscriptionByID(opts, source.ID); err != nil {
		t.Fatalf("subscription metadata not restored: %v", err)
	}
	store, _ := profile.NewStore(opts.profileStorePath)
	profiles, err := store.List()
	if err != nil || len(profiles) != 1 || profiles[0].Name != "stable-delete" {
		t.Fatalf("profile rollback failed: %#v err=%v", profiles, err)
	}
}

func importDeleteSubscription(t *testing.T, opts options, fixturePath string, entries []string) sub.Source {
	t.Helper()
	writeSubscriptionFixture(t, fixturePath, entries)
	if err := runWithOptions(context.Background(), []string{"import", localFileURL(fixturePath)}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("canonical subscription import failed: %v", err)
	}
	storePath, err := resolvedSubscriptionStorePath(opts)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := sub.NewStore(storePath)
	sources, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSuffix(filepath.Base(fixturePath), filepath.Ext(fixturePath))
	for _, source := range sources {
		if strings.Contains(strings.ToLower(source.Name), strings.ToLower(base)) {
			return source
		}
	}
	if len(sources) == 1 {
		return sources[0]
	}
	t.Fatalf("could not resolve imported subscription for %s: %#v", fixturePath, sources)
	return sub.Source{}
}

func onlySubscriptionByID(opts options, id string) (sub.Source, error) {
	storePath, err := resolvedSubscriptionStorePath(opts)
	if err != nil {
		return sub.Source{}, err
	}
	store, err := sub.NewStore(storePath)
	if err != nil {
		return sub.Source{}, err
	}
	return store.Get(id)
}

func profileNames(profiles []profile.Profile) map[string]bool {
	out := make(map[string]bool, len(profiles))
	for _, p := range profiles {
		out[p.Name] = true
	}
	return out
}
