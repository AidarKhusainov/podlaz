package daemon

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDaemonLockAllowsStaleLockFileAfterCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "podlazd.lock")

	first, err := acquireDaemonLock(path)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("release first lock: %v", err)
	}

	second, err := acquireDaemonLock(path)
	if err != nil {
		t.Fatalf("stale lock file must not block restarted daemon: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("release second lock: %v", err)
	}
}

func TestDaemonLockRejectsSecondLiveOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "podlazd.lock")

	first, err := acquireDaemonLock(path)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer first.Close()

	second, err := acquireDaemonLock(path)
	if err == nil {
		_ = second.Close()
		t.Fatal("expected second live daemon lock to fail")
	}
	if !errors.Is(err, errDaemonLockHeld) {
		t.Fatalf("expected errDaemonLockHeld, got %v", err)
	}
}
