package sub

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestSourceWorkflowImportAndUpdateUseSharedPipeline(t *testing.T) {
	dir := t.TempDir()
	profileStore, subscriptionStore := newSourceWorkflowStores(t, dir)
	fixturePath := filepath.Join(dir, "workflow-sub.txt")
	writeSourceWorkflowFixture(t, fixturePath, workflowShareLink(1, "workflow.example", "443", "stable"))

	importedAt := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	imported, err := ImportSource(context.Background(), subscriptionStore, profileStore, localSourceWorkflowFileURL(fixturePath), SourceWorkflowOptions{
		Now: func() time.Time { return importedAt },
	})
	if err != nil {
		t.Fatalf("import source workflow failed: %v", err)
	}
	if imported.Imported != 1 || imported.Updated != 0 || imported.Removed != 0 || imported.Unsupported != 0 {
		t.Fatalf("unexpected import diff: %+v", imported)
	}
	if imported.Subscription.Format != FormatBase64 || !imported.Subscription.LastUpdatedAt.Equal(importedAt) {
		t.Fatalf("import did not persist detected format/time: %+v", imported.Subscription)
	}

	profiles, err := profileStore.List()
	if err != nil {
		t.Fatalf("list imported profiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Name != "stable" {
		t.Fatalf("unexpected imported profiles: %+v", profiles)
	}

	writeSourceWorkflowFixture(t, fixturePath, workflowShareLink(1, "workflow.example", "443", "refreshed"))
	updatedAt := importedAt.Add(time.Hour)
	updated, err := UpdateSource(context.Background(), subscriptionStore, profileStore, imported.Subscription.ID, SourceWorkflowOptions{
		Now: func() time.Time { return updatedAt },
	})
	if err != nil {
		t.Fatalf("update source workflow failed: %v", err)
	}
	if updated.Imported != 0 || updated.Updated != 1 || updated.Unchanged != 0 || updated.Removed != 0 || updated.Unsupported != 0 {
		t.Fatalf("unexpected update diff: %+v", updated)
	}
	if updated.Subscription.Format != FormatBase64 || !updated.Subscription.LastUpdatedAt.Equal(updatedAt) {
		t.Fatalf("update did not persist detected format/time: %+v", updated.Subscription)
	}

	profiles, err = profileStore.List()
	if err != nil {
		t.Fatalf("list updated profiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Name != "refreshed" {
		t.Fatalf("unexpected updated profiles: %+v", profiles)
	}
	if len(updated.Subscription.ProfileIDs) != 1 || updated.Subscription.ProfileIDs[0] != profiles[0].ID {
		t.Fatalf("subscription metadata did not track updated profile ids: source=%+v profiles=%+v", updated.Subscription, profiles)
	}
}

func TestSourceWorkflowImportRollbackRemovesNewSubscriptionAndProfiles(t *testing.T) {
	dir := t.TempDir()
	profileStore, subscriptionStore := newSourceWorkflowStores(t, dir)
	fixturePath := filepath.Join(dir, "import-rollback-sub.txt")
	writeSourceWorkflowFixture(t, fixturePath, workflowShareLink(1, "import-rollback.example", "443", "transient"))

	injectedErr := errors.New("injected import metadata persistence failure")
	_, err := ImportSource(context.Background(), subscriptionStore, profileStore, localSourceWorkflowFileURL(fixturePath), SourceWorkflowOptions{
		AfterProfileApply: func() error { return injectedErr },
	})
	if !errors.Is(err, injectedErr) {
		t.Fatalf("expected injected import failure, got %v", err)
	}

	sources, err := subscriptionStore.List()
	if err != nil {
		t.Fatalf("list subscriptions after import rollback: %v", err)
	}
	if len(sources) != 0 {
		t.Fatalf("failed import left subscription metadata behind: %+v", sources)
	}

	profiles, err := profileStore.List()
	if err != nil {
		t.Fatalf("list profiles after import rollback: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("failed import left profiles behind: %+v", profiles)
	}
}

func TestSourceWorkflowUpdateRollbackRestoresProfilesAndMetadata(t *testing.T) {
	dir := t.TempDir()
	profileStore, subscriptionStore := newSourceWorkflowStores(t, dir)
	fixturePath := filepath.Join(dir, "rollback-sub.txt")
	writeSourceWorkflowFixture(t, fixturePath, workflowShareLink(1, "rollback.example", "443", "last-good"))

	lastGoodAt := time.Date(2026, 6, 30, 13, 0, 0, 0, time.UTC)
	lastGood, err := ImportSource(context.Background(), subscriptionStore, profileStore, localSourceWorkflowFileURL(fixturePath), SourceWorkflowOptions{
		Now: func() time.Time { return lastGoodAt },
	})
	if err != nil {
		t.Fatalf("seed source workflow failed: %v", err)
	}

	injectedErr := errors.New("injected subscription metadata failure")
	writeSourceWorkflowFixture(t, fixturePath, workflowShareLink(1, "rollback.example", "443", "mutated"))
	_, err = UpdateSource(context.Background(), subscriptionStore, profileStore, lastGood.Subscription.ID, SourceWorkflowOptions{
		AfterProfileApply: func() error { return injectedErr },
		Now:               func() time.Time { return lastGoodAt.Add(time.Hour) },
	})
	if !errors.Is(err, injectedErr) {
		t.Fatalf("expected injected failure, got %v", err)
	}

	profiles, err := profileStore.List()
	if err != nil {
		t.Fatalf("list profiles after rollback: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Name != "last-good" {
		t.Fatalf("rollback did not restore last-known-good profile state: %+v", profiles)
	}

	source, err := subscriptionStore.Get(lastGood.Subscription.ID)
	if err != nil {
		t.Fatalf("get subscription after rollback: %v", err)
	}
	if source.Name != lastGood.Subscription.Name || source.Format != lastGood.Subscription.Format || !source.LastUpdatedAt.Equal(lastGoodAt) {
		t.Fatalf("rollback did not preserve last-known-good subscription metadata: got %+v want %+v", source, lastGood.Subscription)
	}
	if fmt.Sprint(source.ProfileIDs) != fmt.Sprint(lastGood.Subscription.ProfileIDs) {
		t.Fatalf("rollback changed subscription profile ids: got %v want %v", source.ProfileIDs, lastGood.Subscription.ProfileIDs)
	}
}

func TestSourceWorkflowImportPropagatesProviderMetadataWarnings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	profileStore, subscriptionStore := newSourceWorkflowStores(t, dir)
	encoded := base64.StdEncoding.EncodeToString([]byte(workflowShareLink(1, "warning.example", "443", "safe-profile")))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Subscription-Title", "https://provider.example/sub/provider-token-123456789")
		_, _ = w.Write([]byte(encoded))
	}))
	defer server.Close()

	result, err := ImportSource(context.Background(), subscriptionStore, profileStore, server.URL, SourceWorkflowOptions{})
	if err != nil {
		t.Fatalf("import source workflow with provider metadata warning failed: %v", err)
	}
	if result.Warnings == nil || len(result.Warnings) != 1 {
		t.Fatalf("expected one propagated provider metadata warning, got %+v", result.Warnings)
	}
	if result.Warnings[0].Message != SubscriptionDisplayNameRejectedWarning {
		t.Fatalf("unexpected provider metadata warning: %+v", result.Warnings[0])
	}
	if strings.Contains(result.Subscription.Name, "provider-token-123456789") {
		t.Fatalf("unsafe provider title was used as display name: %+v", result.Subscription)
	}
}

func newSourceWorkflowStores(t *testing.T, dir string) (profile.Store, Store) {
	t.Helper()
	profileStore, err := profile.NewStore(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatalf("new profile store: %v", err)
	}
	subscriptionStore, err := NewStore(filepath.Join(dir, "subscriptions.json"))
	if err != nil {
		t.Fatalf("new subscription store: %v", err)
	}
	return profileStore, subscriptionStore
}

func writeSourceWorkflowFixture(t *testing.T, path string, entries ...string) {
	t.Helper()
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Join(entries, "\n")))
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		t.Fatalf("write subscription fixture: %v", err)
	}
}

func localSourceWorkflowFileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func workflowShareLink(n int, host, port, name string) string {
	return "vl" + "ess://" + workflowUUIDForTest(n) + "@" + host + ":" + port + "?type=tcp&security=tls&encryption=none#" + name
}

func workflowUUIDForTest(n int) string {
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", n)
}
