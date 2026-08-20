package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func testNetworkSessionProtection() networkSessionProtection {
	return networkSessionProtection{
		State:              networkSessionProtectionArmed,
		CompositionVersion: 1,
		Family:             "inet",
		Table:              "podlaz_pe_001122334455",
		TunInterface:       "podlaz0",
		BootstrapIPv4:      []string{"192.0.2.10"},
	}
}

func TestNetworkSessionStateBeginOrResumeKeepsStablePrivateIdentity(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	request := testContinuationRequest()

	first, err := store.BeginOrResume(request)
	if err != nil {
		t.Fatalf("begin network session: %v", err)
	}
	if first.SessionID == "" {
		t.Fatal("network session id must be persisted before protected lifecycle mutation")
	}
	if first.Intent != networkSessionIntentResume {
		t.Fatalf("initial intent = %q, want %q", first.Intent, networkSessionIntentResume)
	}

	second, err := store.BeginOrResume(request)
	if err != nil {
		t.Fatalf("resume network session: %v", err)
	}
	if second.SessionID != first.SessionID {
		t.Fatalf("session id changed across same-boot resume: first=%q second=%q", first.SessionID, second.SessionID)
	}

	info, err := os.Stat(filepath.Join(runtimeDir, networkSessionContinuationFileName))
	if err != nil {
		t.Fatalf("stat network session state: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("network session state mode = %o, want 600", got)
	}
}

func TestNetworkSessionStateTerminalIntentKeepsExactProtectionAuthority(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin network session: %v", err)
	}
	protection := testNetworkSessionProtection()
	if err := store.SetProtection(&protection); err != nil {
		t.Fatalf("persist protection authority: %v", err)
	}
	if err := store.SetIntent(networkSessionIntentTerminal); err != nil {
		t.Fatalf("persist terminal intent: %v", err)
	}

	got, ok, err := store.Load()
	if err != nil {
		t.Fatalf("load terminal network session state: %v", err)
	}
	if !ok {
		t.Fatal("terminal network session state must retain cleanup authority")
	}
	if got.Intent != networkSessionIntentTerminal {
		t.Fatalf("intent = %q, want terminal", got.Intent)
	}
	if got.Protection == nil {
		t.Fatal("terminal intent must not erase exact protection authority")
	}
	if !reflect.DeepEqual(*got.Protection, protection) {
		t.Fatalf("protection authority changed: got %#v want %#v", *got.Protection, protection)
	}

	continuation := newNetworkSessionContinuationStore(runtimeDir, fixedBootID("boot-a"))
	if _, exists, err := continuation.LoadCurrent(); err != nil {
		t.Fatalf("load continuation after terminal decision: %v", err)
	} else if exists {
		t.Fatal("terminal session must not remain armed for automatic reconnect")
	}

	after, ok, err := store.Load()
	if err != nil || !ok || after.Protection == nil {
		t.Fatalf("reading disarmed continuation must preserve cleanup authority, ok=%v protection=%#v err=%v", ok, after.Protection, err)
	}
}

func TestNetworkSessionStateRefusesRemovalWhileProtectionAuthorityExists(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin network session: %v", err)
	}
	protection := testNetworkSessionProtection()
	if err := store.SetProtection(&protection); err != nil {
		t.Fatalf("persist protection authority: %v", err)
	}

	if err := store.Remove(); err == nil {
		t.Fatal("session state removal must fail while exact protection authority exists")
	}
	got, ok, err := store.Load()
	if err != nil {
		t.Fatalf("load state after refused removal: %v", err)
	}
	if !ok || got.Protection == nil {
		t.Fatalf("refused removal lost protection authority: ok=%v state=%#v", ok, got)
	}
}

func TestNetworkSessionStateCanBeRemovedAfterProtectionAuthorityCleared(t *testing.T) {
	runtimeDir := t.TempDir()
	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := store.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin network session: %v", err)
	}
	protection := testNetworkSessionProtection()
	if err := store.SetProtection(&protection); err != nil {
		t.Fatalf("persist protection authority: %v", err)
	}
	if err := store.SetIntent(networkSessionIntentDisconnect); err != nil {
		t.Fatalf("persist disconnect intent: %v", err)
	}
	if err := store.SetProtection(nil); err != nil {
		t.Fatalf("clear verified protection authority: %v", err)
	}
	if err := store.Remove(); err != nil {
		t.Fatalf("remove finalized network session state: %v", err)
	}
	if _, ok, err := store.Load(); err != nil || ok {
		t.Fatalf("finalized state must be absent, ok=%v err=%v", ok, err)
	}
}

func TestNetworkSessionStateMalformedProtectionFailsClosedWithoutDeletingEvidence(t *testing.T) {
	runtimeDir := t.TempDir()
	path := filepath.Join(runtimeDir, networkSessionContinuationFileName)
	const malformed = `{
  "schema_version": "podlaz.network-session-state.v1",
  "owner": "podlaz",
  "boot_id": "boot-a",
  "session_id": "00112233445566778899aabbccddeeff",
  "intent": "terminal",
  "request": {
    "mode": "tun",
    "profile": {
      "id": "profile-example",
      "name": "Example profile",
      "source": "manual",
      "engine": "xray",
      "server": "vpn.example.test",
      "port": 443,
      "protocol": "vless",
      "user_identity": "00000000-0000-4000-8000-000000000001"
    },
    "handoff": "block"
  },
  "protection": {
    "state": "armed",
    "composition_version": 1,
    "family": "inet",
    "table": "foreign_table",
    "tun_interface": "podlaz0",
    "bootstrap_ipv4": ["192.0.2.10"]
  }
}`
	if err := os.WriteFile(path, []byte(malformed), 0o600); err != nil {
		t.Fatalf("write malformed session state: %v", err)
	}

	store := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, _, err := store.Load(); err == nil {
		t.Fatal("malformed protection authority must fail closed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("invalid authority evidence must be retained for diagnosis/recovery instead of silently discarded: %v", err)
	}
}

func TestNetworkSessionStatePreviousBootDoesNotGrantCurrentCleanupAuthority(t *testing.T) {
	runtimeDir := t.TempDir()
	old := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-a"))
	if _, err := old.BeginOrResume(testContinuationRequest()); err != nil {
		t.Fatalf("begin old network session: %v", err)
	}

	current := newNetworkSessionStateStore(runtimeDir, fixedBootID("boot-b"))
	_, ok, err := current.Load()
	if err != nil {
		t.Fatalf("load previous-boot state: %v", err)
	}
	if ok {
		t.Fatal("previous-boot volatile state must not become current cleanup authority")
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, networkSessionContinuationFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("previous-boot state must be discarded without mutating current host state, stat error: %v", err)
	}
}
