package profile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewManualProfileValidation(t *testing.T) {
	p := NewManual("Test Profile", "example.com", 443, "vless")
	if p.ID != "test-profile" {
		t.Fatalf("expected normalized ID, got %q", p.ID)
	}
	if err := Validate(p); err != nil {
		t.Fatalf("expected valid manual profile: %v", err)
	}
}

func TestValidateRejectsInvalidProfile(t *testing.T) {
	err := Validate(Profile{Name: "bad", Source: SourceManual, Engine: EngineXray, Server: "bad host", Protocol: "ftp"})
	if err == nil {
		t.Fatal("expected validation error")
	}
	for _, want := range []string{"id is required", "unsupported protocol", "server must not contain whitespace", "port must be between"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected %q in %q", want, err.Error())
		}
	}
}

func TestDefaultStorePathIgnoresRelativeXDGStateHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "relative-state")

	got, err := DefaultStorePath()
	if err != nil {
		t.Fatalf("default store path: %v", err)
	}
	want := filepath.Join(home, ".local", "state", "tunwarden", "profiles.json")
	if got != want {
		t.Fatalf("expected fallback path %q, got %q", want, got)
	}
}

func TestStorePersistsProfilesAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	want := NewManual("test", "example.com", 443, "vless")
	if err := store.Add(want); err != nil {
		t.Fatalf("add profile: %v", err)
	}

	reopened, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	got, err := reopened.Get("test")
	if err != nil {
		t.Fatalf("get profile after reopen: %v", err)
	}
	if got != want {
		t.Fatalf("stored profile mismatch: got %#v want %#v", got, want)
	}
}

func TestStoreRejectsDuplicateProfiles(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	p := NewManual("test", "example.com", 443, "vless")
	if err := store.Add(p); err != nil {
		t.Fatalf("add profile: %v", err)
	}
	if err := store.Add(p); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestStoreDeleteRemovesProfile(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	p := NewManual("test", "example.com", 443, "vless")
	if err := store.Add(p); err != nil {
		t.Fatalf("add profile: %v", err)
	}
	if err := store.Delete("test"); err != nil {
		t.Fatalf("delete profile: %v", err)
	}
	if _, err := store.Get("test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreFailsSafelyOnCorruptStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("write corrupt store: %v", err)
	}
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	_, err = store.List()
	if err == nil {
		t.Fatal("expected corrupt storage to fail")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected clear invalid JSON error, got %v", err)
	}
}

func TestStoreWritesRestrictivePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Add(NewManual("test", "example.com", 443, "vless")); err != nil {
		t.Fatalf("add profile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat store: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected profile store mode 0600, got %o", got)
	}
}
