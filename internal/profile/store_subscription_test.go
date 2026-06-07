package profile

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreReplaceSubscriptionProfilesPreservesManualAndDiffs(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	manual := NewManual("manual", "manual.example", 443, "vmess")
	if err := store.Add(manual); err != nil {
		t.Fatalf("add manual profile: %v", err)
	}

	first := subscriptionProfile("sub-a", "sub-a.example")
	diff, err := store.ReplaceSubscriptionProfiles(nil, []Profile{first})
	if err != nil {
		t.Fatalf("initial replace failed: %v", err)
	}
	if diff.Imported != 1 || diff.Updated != 0 || diff.Unchanged != 0 || diff.Removed != 0 {
		t.Fatalf("unexpected initial diff: %#v", diff)
	}

	diff, err = store.ReplaceSubscriptionProfiles([]string{"sub-a"}, []Profile{first})
	if err != nil {
		t.Fatalf("same replace failed: %v", err)
	}
	if diff.Imported != 0 || diff.Updated != 0 || diff.Unchanged != 1 || diff.Removed != 0 {
		t.Fatalf("unexpected unchanged diff: %#v", diff)
	}

	changed := first
	changed.Server = "sub-b.example"
	diff, err = store.ReplaceSubscriptionProfiles([]string{"sub-a"}, []Profile{changed})
	if err != nil {
		t.Fatalf("changed replace failed: %v", err)
	}
	if diff.Imported != 0 || diff.Updated != 1 || diff.Unchanged != 0 || diff.Removed != 0 {
		t.Fatalf("unexpected changed diff: %#v", diff)
	}

	profiles, err := store.List()
	if err != nil {
		t.Fatalf("list profiles: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("expected manual plus subscription profile, got %#v", profiles)
	}
}

func TestStoreReplaceSubscriptionProfilesRejectsExistingProfileCollision(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	manual := NewManual("manual", "manual.example", 443, "vmess")
	if err := store.Add(manual); err != nil {
		t.Fatalf("add manual profile: %v", err)
	}

	p := subscriptionProfile(manual.ID, "sub.example")
	_, err = store.ReplaceSubscriptionProfiles(nil, []Profile{p})
	if err == nil {
		t.Fatal("expected profile id collision to fail")
	}
	if !strings.Contains(err.Error(), "profile id collision") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func subscriptionProfile(id, server string) Profile {
	return Profile{
		ID:       id,
		Name:     id,
		Source:   SourceSubscription,
		Engine:   EngineXray,
		Server:   server,
		Port:     443,
		Protocol: "vmess",
	}
}
