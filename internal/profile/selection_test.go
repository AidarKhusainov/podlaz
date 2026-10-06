package profile

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestStoreResolvePrefersExactStableIDOverName(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	idMatch := NewManual("Display One", "one.example", 443, "vless")
	idMatch.ID = "work"
	nameMatch := NewManual("work", "two.example", 443, "vless")
	nameMatch.ID = "other"
	if err := store.Add(idMatch); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(nameMatch); err != nil {
		t.Fatal(err)
	}

	got, err := store.Resolve("work")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != idMatch.ID {
		t.Fatalf("resolved %q, want exact ID %q", got.ID, idMatch.ID)
	}
}

func TestStoreResolveUsesUniqueTrimmedCaseInsensitiveName(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	p := NewManual("Work VPN", "one.example", 443, "vless")
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}

	got, err := store.Resolve("  work vpn ")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID {
		t.Fatalf("resolved %q, want %q", got.ID, p.ID)
	}
}

func TestStoreResolveRejectsAmbiguousDisplayName(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	first := NewManual("Work", "one.example", 443, "vless")
	first.ID = "work-a"
	second := NewManual(" work ", "two.example", 443, "vless")
	second.ID = "work-b"
	if err := store.Add(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(second); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Resolve("WORK"); !errors.Is(err, ErrAmbiguousSelector) {
		t.Fatalf("err=%v, want ErrAmbiguousSelector", err)
	}
}

func TestStoreSelectionPersistsStableID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	store, _ := NewStore(path)
	p := NewManual("Work VPN", "one.example", 443, "vless")
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Select("Work VPN"); err != nil {
		t.Fatal(err)
	}

	reopened, _ := NewStore(path)
	got, err := reopened.ResolveSelected()
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID {
		t.Fatalf("selected ID=%q want=%q", got.ID, p.ID)
	}
}

func TestStoreResolveSelectedAutoSelectsExactlyOneProfile(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	p := NewManual("Only VPN", "one.example", 443, "vless")
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}
	id, err := store.SelectedID()
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("read-only selection lookup mutated state to %q", id)
	}

	got, err := store.ResolveSelected()
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID {
		t.Fatalf("selected=%q want=%q", got.ID, p.ID)
	}
	id, err = store.SelectedID()
	if err != nil || id != p.ID {
		t.Fatalf("persisted selected ID=%q err=%v", id, err)
	}
}

func TestStoreStaleSelectionClearsWithoutNameRetarget(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	first := NewManual("Original", "one.example", 443, "vless")
	first.ID = "first"
	second := NewManual("Replacement", "two.example", 443, "vless")
	second.ID = "second"
	if err := store.saveState(storeFile{
		SchemaVersion:     "v1",
		Profiles:          []Profile{first, second},
		SelectedProfileID: "missing-id",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.ResolveSelected(); !errors.Is(err, ErrNoSelection) {
		t.Fatalf("err=%v, want ErrNoSelection", err)
	}
	id, err := store.SelectedID()
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("stale selection retargeted to %q", id)
	}
}

func TestStoreDeleteSelectedProfileClearsSelectionAtomically(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	first := NewManual("First", "one.example", 443, "vless")
	second := NewManual("Second", "two.example", 443, "vless")
	if err := store.Add(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Select(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(first.ID); err != nil {
		t.Fatal(err)
	}
	state, err := store.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.SelectedProfileID != "" {
		t.Fatalf("delete did not atomically clear selection: %q", state.SelectedProfileID)
	}
	id, err := store.SelectedID()
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("read-only selection lookup silently retargeted to %q", id)
	}
	selected, err := store.ResolveSelected()
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != second.ID {
		t.Fatalf("normal user intent selected %q, want %q", selected.ID, second.ID)
	}
}

func TestStoreSubscriptionRemovalClearsSelectedStableID(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	p := subscriptionProfile("sub-a", "sub.example")
	if _, err := store.ReplaceSubscriptionProfiles(nil, []Profile{p}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Select(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceSubscriptionProfiles([]string{p.ID}, nil); err != nil {
		t.Fatal(err)
	}
	id, err := store.SelectedID()
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Fatalf("removed selected subscription profile left selection %q", id)
	}
}
