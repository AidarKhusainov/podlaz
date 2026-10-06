package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestRunCLISubscriptionDeleteReportsMatchingManualProfilesLeftUntouched(t *testing.T) {
	dir := t.TempDir()
	opts := options{profileStorePath: filepath.Join(dir, "profiles.json")}
	source := importDeleteSubscription(t, opts, filepath.Join(dir, "diag.txt"), []string{
		shareLink(701, "matching-delete.example", "443", "?type=tcp&security=tls", "subscription-owned"),
	})

	store, _ := profile.NewStore(opts.profileStorePath)
	manual := testConnectProfile()
	manual.ID = "manual-match"
	manual.Name = "manual-match"
	manual.Server = "matching-delete.example"
	manual.Source = profile.SourceManual
	if err := store.Add(manual); err != nil {
		t.Fatalf("add matching manual profile: %v", err)
	}

	var deleteOut bytes.Buffer
	if err := runWithOptions(context.Background(), []string{"subscription", "delete", source.ID, "--yes"}, &deleteOut, opts); err != nil {
		t.Fatalf("subscription delete failed: %v", err)
	}
	for _, want := range []string{
		"Subscription deleted: " + source.ID,
		"Profiles removed: 1",
		"Orphan or manual profiles with matching servers were left untouched: 1",
	} {
		if !strings.Contains(deleteOut.String(), want) {
			t.Fatalf("delete output missing %q: %q", want, deleteOut.String())
		}
	}

	profiles, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].ID != manual.ID || profiles[0].Server != manual.Server {
		t.Fatalf("matching manual profile not preserved: %#v", profiles)
	}
}
