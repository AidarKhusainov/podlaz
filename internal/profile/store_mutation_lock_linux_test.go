package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestConfirmedPositionDeleteWaitsForWriterAndRejectsChangedRow(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"b", "c"} {
		if err := store.Add(numberedTestProfile(id, id)); err != nil {
			t.Fatal(err)
		}
	}
	confirmed, err := store.Resolve("2")
	if err != nil {
		t.Fatal(err)
	}

	lock, err := os.OpenFile(store.Path()+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		}
	}()

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- store.DeleteResolved("2", confirmed)
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("delete did not honor concurrent writer lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	// Simulate the lock owner's committed import changing the numbered row
	// while confirmation is pending. The delete must reread after acquiring
	// the same lock and must never overwrite that import.
	state, err := store.loadState()
	if err != nil {
		t.Fatal(err)
	}
	state.Profiles = append(state.Profiles, numberedTestProfile("a", "Inserted"))
	if err := store.saveState(state); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	locked = false

	select {
	case err := <-done:
		if !errors.Is(err, ErrProfileChanged) {
			t.Fatalf("stale row delete error = %v, want ErrProfileChanged", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delete did not finish after lock release")
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, err := store.Get(id); err != nil {
			t.Fatalf("profile %q lost during concurrent deletion: %v", id, err)
		}
	}
}

func TestConcurrentProfileImportsDoNotLoseUpdates(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	const count = 24
	start := make(chan struct{})
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("profile-%02d", i)
		go func() {
			<-start
			errs <- store.Add(numberedTestProfile(id, id))
		}()
	}
	close(start)
	for i := 0; i < count; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	profiles, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != count {
		t.Fatalf("concurrent writers lost updates: got %d profiles, want %d", len(profiles), count)
	}
}
