package profile

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func numberedTestProfile(id, name string) Profile {
	p := NewManual(name, "vpn.example.test", 443, "vless")
	p.ID = id
	return p
}

func TestNumberedSelectorsUseSortedListSnapshot(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	z := numberedTestProfile("zulu", "Кириллица")
	a := numberedTestProfile("alpha", "2")
	numericID := numberedTestProfile("2", "Other")
	if err := store.saveState(storeFile{
		SchemaVersion:     "v1",
		Profiles:          []Profile{z, a, numericID},
		SelectedProfileID: z.ID,
	}); err != nil {
		t.Fatal(err)
	}
	list, selected, err := store.ListWithSelectedID()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].ID != "2" || list[1].ID != a.ID || list[2].ID != z.ID || selected != z.ID {
		t.Fatalf("sorted list=%v, selected=%s", list, selected)
	}
	for selector, want := range map[string]string{
		"1": "2", "2": a.ID, "3": z.ID, " 2 ": a.ID, "id:2": "2", "alpha": a.ID,
	} {
		got, err := store.Resolve(selector)
		if err != nil || got.ID != want {
			t.Fatalf("resolve %q = %q, %v; want %q", selector, got.ID, err, want)
		}
	}
	selectedProfile, err := store.Select("2")
	if err != nil || selectedProfile.ID != a.ID {
		t.Fatalf("Select(2)=%q, %v", selectedProfile.ID, err)
	}
	state, err := store.loadState()
	if err != nil || state.SelectedProfileID != a.ID {
		t.Fatalf("persisted selection=%q, %v", state.SelectedProfileID, err)
	}
	if _, err := store.Resolve("2"); err != nil {
		t.Fatal(err)
	}
}

func TestNumberedSelectorsRejectInvalidNumbersWithoutNameFallback(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	for _, p := range []Profile{
		numberedTestProfile("a", "0"),
		numberedTestProfile("b", "999"),
		numberedTestProfile("c", "1.0"),
	} {
		if err := store.Add(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"0", "4", "999", "999999999999999999999999999999", "-1", "+1", "1.0", "1e2", "0x2", "1_0", "１"} {
		if _, err := store.Resolve(value); !errors.Is(err, ErrInvalidPosition) {
			t.Errorf("Resolve(%q) = %v, want invalid position", value, err)
		}
	}
	if _, err := store.Resolve("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing name err=%v", err)
	}
	if _, err := store.Resolve("id:missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing ID err=%v", err)
	}
}

func TestNumberedSelectorReorderDoesNotPersistPosition(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	first, second := numberedTestProfile("b", "First"), numberedTestProfile("c", "Second")
	if err := store.Add(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Select("2"); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(numberedTestProfile("a", "New")); err != nil {
		t.Fatal(err)
	}
	id, err := store.SelectedID()
	if err != nil || id != second.ID {
		t.Fatalf("position retargeted selected ID to %q: %v", id, err)
	}
	p, err := store.Resolve("2")
	if err != nil || p.ID != first.ID {
		t.Fatalf("post-insertion row 2=%q, %v", p.ID, err)
	}
}

func TestNumberedDeleteRequiresUnchangedConfirmedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(Store, Profile) error
	}{
		{"profile edited", func(s Store, p Profile) error {
			state, err := s.loadState()
			if err != nil {
				return err
			}
			for i := range state.Profiles {
				if state.Profiles[i].ID == p.ID {
					state.Profiles[i].Name = "Changed"
				}
			}
			return s.saveState(state)
		}},
		{"row reordered", func(s Store, p Profile) error { return s.Add(numberedTestProfile("a", "Inserted")) }},
		{"profile disappeared", func(s Store, p Profile) error { return s.Delete(p.ID) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
			first, second := numberedTestProfile("b", "Первый"), numberedTestProfile("c", "Второй")
			if err := store.Add(first); err != nil {
				t.Fatal(err)
			}
			if err := store.Add(second); err != nil {
				t.Fatal(err)
			}
			chosen, err := store.Resolve("2")
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.mutate(store, chosen); err != nil {
				t.Fatal(err)
			}
			if err := store.DeleteResolved("2", chosen); !errors.Is(err, ErrProfileChanged) {
				t.Fatalf("delete of stale choice err=%v", err)
			}
			if tc.name != "profile disappeared" {
				if _, err := store.Get(second.ID); err != nil {
					t.Fatalf("stale delete removed original: %v", err)
				}
			}
		})
	}
	store, _ := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	p := numberedTestProfile("safe", "Safe")
	if err := store.Add(p); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteResolved("1", p); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted still present: %v", err)
	}
}

func TestNumberedSelectorKeepsAmbiguousNameRules(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	if err := store.Add(numberedTestProfile("b", "Один")); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(numberedTestProfile("a", " один ")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve("ОДИН"); !errors.Is(err, ErrAmbiguousSelector) {
		t.Fatalf("ambiguity=%v", err)
	}
	for i, want := range []string{"a", "b"} {
		p, err := store.Resolve(strings.TrimSpace(string(rune('1' + i))))
		if err != nil || p.ID != want {
			t.Fatalf("row %d = %q, %v", i+1, p.ID, err)
		}
	}
}
