package daemon

import (
	"os"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
)

func TestProductTerminalReasonStoreRoundTripsCurrentBootPrivately(t *testing.T) {
	dir := t.TempDir()
	store := newProductTerminalReasonStore(dir, fixedBootID(testBootAttempt))
	if err := store.Set(api.TerminalReasonVPNRestoreFailed); err != nil {
		t.Fatal(err)
	}
	reason, exists, err := store.LoadCurrent()
	if err != nil || !exists || reason != api.TerminalReasonVPNRestoreFailed {
		t.Fatalf("reason=%q exists=%v err=%v", reason, exists, err)
	}
	info, err := os.Stat(store.path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("terminal reason mode=%o, want 600", got)
	}
}

func TestProductTerminalReasonStoreSupersedeBlocksOlderOutcome(t *testing.T) {
	store := newProductTerminalReasonStore(t.TempDir(), fixedBootID(testBootAttempt))
	if err := store.Set(api.TerminalReasonVPNConnectFailed); err != nil {
		t.Fatal(err)
	}
	if err := store.Supersede(); err != nil {
		t.Fatal(err)
	}
	resolution, exists, err := store.ResolveCurrent()
	if err != nil || !exists {
		t.Fatalf("supersede resolution exists=%v err=%v", exists, err)
	}
	if !resolution.Superseded || resolution.Reason != "" {
		t.Fatalf("supersede resolution=%+v", resolution)
	}
	if reason, exists, err := store.LoadCurrent(); err != nil || exists || reason != "" {
		t.Fatalf("superseded reason remained loadable: reason=%q exists=%v err=%v", reason, exists, err)
	}
}

func TestProductTerminalReasonStoreNewTerminalOutcomeReplacesSupersede(t *testing.T) {
	store := newProductTerminalReasonStore(t.TempDir(), fixedBootID(testBootAttempt))
	if err := store.Supersede(); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(api.TerminalReasonVPNRestoreFailed); err != nil {
		t.Fatal(err)
	}
	resolution, exists, err := store.ResolveCurrent()
	if err != nil || !exists {
		t.Fatalf("replacement resolution exists=%v err=%v", exists, err)
	}
	if resolution.Superseded || resolution.Reason != api.TerminalReasonVPNRestoreFailed {
		t.Fatalf("replacement resolution=%+v", resolution)
	}
}

func TestProductTerminalReasonStoreRejectsReasonAndSupersedeTogether(t *testing.T) {
	store := newProductTerminalReasonStore(t.TempDir(), fixedBootID(testBootAttempt))
	data := []byte(`{"schema_version":"podlaz.product-terminal-reason.v1","boot_id":"boot-attempt","reason":"vpn_restore_failed","superseded":true}` + "\n")
	if err := os.WriteFile(store.path(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ResolveCurrent(); err == nil {
		t.Fatal("expected ambiguous reason+superseded state to be rejected")
	}
}

func TestProductTerminalReasonStoreDiscardsPreviousBoot(t *testing.T) {
	dir := t.TempDir()
	old := newProductTerminalReasonStore(dir, fixedBootID(testBootConfigured))
	if err := old.Set(api.TerminalReasonVPNConnectFailed); err != nil {
		t.Fatal(err)
	}
	current := newProductTerminalReasonStore(dir, fixedBootID(testBootAttempt))
	if reason, exists, err := current.LoadCurrent(); err != nil || exists || reason != "" {
		t.Fatalf("previous boot reason survived: reason=%q exists=%v err=%v", reason, exists, err)
	}
	if _, err := os.Stat(current.path()); !os.IsNotExist(err) {
		t.Fatalf("previous boot reason file was not removed: %v", err)
	}
}

func TestProductTerminalReasonStoreClearRemovesReason(t *testing.T) {
	store := newProductTerminalReasonStore(t.TempDir(), fixedBootID(testBootAttempt))
	if err := store.Set(api.TerminalReasonBootNetworkNotReady); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if reason, exists, err := store.LoadCurrent(); err != nil || exists || reason != "" {
		t.Fatalf("reason remained after clear: reason=%q exists=%v err=%v", reason, exists, err)
	}
}

func TestProductTerminalReasonStoreRejectsUnknownReason(t *testing.T) {
	store := newProductTerminalReasonStore(t.TempDir(), fixedBootID(testBootAttempt))
	if err := store.Set(api.TerminalReason("arbitrary_error_text")); err == nil {
		t.Fatal("expected unknown terminal reason to be rejected")
	}
}
