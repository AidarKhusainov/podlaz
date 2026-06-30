package sub

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStorePersistsSubscriptionsAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscriptions.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	want := validStoreSource("provider")
	if err := store.Add(want); err != nil {
		t.Fatalf("add subscription: %v", err)
	}

	reopened, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	got, err := reopened.Get("provider")
	if err != nil {
		t.Fatalf("get subscription after reopen: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored subscription mismatch: got %#v want %#v", got, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read subscription store: %v", err)
	}
	for _, wantFragment := range []string{`"schema_version": "v1"`, `"subscriptions"`} {
		if !strings.Contains(string(data), wantFragment) {
			t.Fatalf("expected subscription store JSON to contain %s, got:\n%s", wantFragment, data)
		}
	}
}

func TestStoreWritesRestrictivePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscriptions.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Add(validStoreSource("provider")); err != nil {
		t.Fatalf("add subscription: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat store: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected subscription store mode 0600, got %o", got)
	}
}

func TestStoreSyncsParentDirectoryAfterAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subscriptions.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	syncCalled := false
	syncParentDir := func(gotDir string) error {
		syncCalled = true
		if gotDir != dir {
			return fmt.Errorf("sync dir = %q, want %q", gotDir, dir)
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("subscription store was not renamed before directory sync: %w", err)
		}
		return nil
	}

	if err := store.saveWithDirectorySync([]Source{validStoreSource("provider")}, syncParentDir); err != nil {
		t.Fatalf("save subscription: %v", err)
	}
	if !syncCalled {
		t.Fatal("expected subscription store parent directory sync after atomic replace")
	}
}

func TestStoreKeepsRenamedSubscriptionStoreWhenDirectorySyncFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subscriptions.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	syncParentDir := func(string) error {
		return errors.New("forced sync failure")
	}

	err = store.saveWithDirectorySync([]Source{validStoreSource("provider")}, syncParentDir)
	if err == nil {
		t.Fatal("expected save subscription to report directory sync failure")
	}
	for _, want := range []string{"sync subscription store parent directory", "forced sync failure"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected %q in %q", want, err.Error())
		}
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected renamed subscription store to remain after directory sync failure: %v", err)
	}

	reopened, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	if _, err := reopened.Get("provider"); err != nil {
		t.Fatalf("expected renamed subscription store to remain readable after directory sync failure: %v", err)
	}

	leftovers, err := filepath.Glob(filepath.Join(dir, ".subscriptions-*.tmp"))
	if err != nil {
		t.Fatalf("glob temporary subscription stores: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("expected temporary subscription store cleanup, found %v", leftovers)
	}
}

func validStoreSource(id string) Source {
	return Source{
		ID:     id,
		Name:   id,
		URL:    "https://provider.example/subscription",
		Format: FormatBase64,
	}
}
