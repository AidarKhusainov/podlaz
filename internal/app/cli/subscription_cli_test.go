package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/sub"
)

func TestRunCLICanonicalImportThenSubscriptionListShowUpdate(t *testing.T) {
	dir := t.TempDir()
	profileStorePath := filepath.Join(dir, "profiles.json")
	fixturePath := filepath.Join(dir, "sub.txt")
	writeSubscriptionFixture(t, fixturePath, []string{
		shareLink(1, "one.example", "443", "?type=tcp&security=tls&encryption=none&ignored=value", "one"),
		"unsupported://unsupported",
		shareLink(2, "two.example", "8443", "?type=grpc&security=tls&serviceName=svc", "two"),
	})
	opts := options{profileStorePath: profileStorePath}

	var importOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"import", localFileURL(fixturePath)}, &importOut, opts); err != nil {
		t.Fatalf("canonical import failed: %v", err)
	}
	for _, want := range []string{"Subscription imported", "Profiles: 2"} {
		if !strings.Contains(importOut.String(), want) {
			t.Fatalf("import output missing %q: %q", want, importOut.String())
		}
	}
	if strings.Contains(importOut.String(), localFileURL(fixturePath)) || strings.Contains(importOut.String(), uuidForTest(1)) {
		t.Fatalf("import leaked source identity: %q", importOut.String())
	}

	storePath, err := resolvedSubscriptionStorePath(opts)
	if err != nil {
		t.Fatal(err)
	}
	subStore, err := sub.NewStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := subStore.List()
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%#v err=%v", sources, err)
	}
	source := sources[0]

	var listOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "list"}, &listOut, opts); err != nil {
		t.Fatalf("subscription list: %v", err)
	}
	if !strings.Contains(listOut.String(), source.Name) || strings.Contains(listOut.String(), source.URL) {
		t.Fatalf("subscription list output=%q", listOut.String())
	}

	var showOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "show", source.ID}, &showOut, opts); err != nil {
		t.Fatalf("subscription show: %v", err)
	}
	if strings.Contains(showOut.String(), source.URL) || strings.Contains(showOut.String(), "URL:") {
		t.Fatalf("subscription show leaked URL: %q", showOut.String())
	}
	for _, want := range []string{"Name: " + source.Name, "Imported profiles: 2"} {
		if !strings.Contains(showOut.String(), want) {
			t.Fatalf("show missing %q: %q", want, showOut.String())
		}
	}

	var updateOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "update", source.ID}, &updateOut, opts); err != nil {
		t.Fatalf("subscription update: %v", err)
	}
	for _, want := range []string{"Subscription updated:", "Imported: 0", "Unchanged: 2", "Unsupported: 1"} {
		if !strings.Contains(updateOut.String(), want) {
			t.Fatalf("update missing %q: %q", want, updateOut.String())
		}
	}
	if strings.Contains(updateOut.String(), uuidForTest(1)) {
		t.Fatalf("update leaked identity: %q", updateOut.String())
	}
}

func TestRunCLISubscriptionUpdateRollbackPreservesLastKnownGood(t *testing.T) {
	dir := t.TempDir()
	profileStorePath := filepath.Join(dir, "profiles.json")
	fixturePath := filepath.Join(dir, "sub.txt")
	writeSubscriptionFixture(t, fixturePath, []string{shareLink(1, "stable.example", "443", "?type=tcp&security=tls", "stable")})
	opts := options{profileStorePath: profileStorePath}

	if err := runWithOptions(context.Background(), []string{"import", localFileURL(fixturePath)}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("initial import: %v", err)
	}
	storePath, _ := resolvedSubscriptionStorePath(opts)
	subStore, _ := sub.NewStore(storePath)
	sources, err := subStore.List()
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%#v err=%v", sources, err)
	}
	sourceID := sources[0].ID

	writeSubscriptionFixture(t, fixturePath, []string{shareLink(1, "changed.example", "443", "?type=tcp&security=tls", "stable")})
	subscriptionAfterProfileApplyHook = func() error { return fmt.Errorf("injected subscription metadata failure") }
	defer func() { subscriptionAfterProfileApplyHook = nil }()

	err = runWithOptions(context.Background(), []string{"subscription", "update", sourceID}, &bytes.Buffer{}, opts)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("update err=%v exit=%d", err, ExitCode(err))
	}

	profileStore, _ := profile.NewStore(profileStorePath)
	profiles, err := profileStore.List()
	if err != nil || len(profiles) != 1 {
		t.Fatalf("profiles=%#v err=%v", profiles, err)
	}
	if profiles[0].Server != "stable.example" {
		t.Fatalf("rollback published %q, want stable.example", profiles[0].Server)
	}
}

func TestRunCLISubscriptionManualAddAndJSONSurfacesAreRemoved(t *testing.T) {
	for _, args := range [][]string{
		{"subscription", "add", "--name", "test", "--url", "file:///tmp/test"},
		{"subscription", "list", "--json"},
		{"subscription", "show", "test", "--json"},
	} {
		err := runWithOptions(context.Background(), args, &bytes.Buffer{}, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("args=%v err=%v exit=%d, want usage error", args, err, ExitCode(err))
		}
	}
}

func TestRunCLISubscriptionInvalidUsageExitCode(t *testing.T) {
	err := runWithOptions(context.Background(), []string{"subscription", "update"}, &bytes.Buffer{}, options{profileStorePath: filepath.Join(t.TempDir(), "profiles.json")})
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
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
