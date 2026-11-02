//go:build linux

package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestNML3ReadOnlyIntentRecoveryPreservesExactProgress(t *testing.T) {
	for _, stage := range []string{"planned", "profile-added", "activating"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			session := protectedNMTestSession(t, dir)
			store := newNML3Stager(dir, fixedBootID("boot-a"))
			intent := nmL3Intent{
				Schema:    nmL3IntentSchema,
				Owner:     networkSessionStateOwner,
				BootID:    "boot-a",
				SessionID: session.SessionID,
				UUID:      "abcdef00-0000-4000-8000-000000000002",
				Interface: "podlaz0",
				IPv4:      "198.18.0.17/32",
				DNS:       "1.1.1.1",
				MTU:       1500, Metric: 10,
				Mark:  101,
				Stage: stage,
			}
			if stage == "profile-added" || stage == "activating" {
				intent.SettingsPath = dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings/7")
			}
			if stage == "activating" {
				intent.ActivePath = dbus.ObjectPath("/org/freedesktop/NetworkManager/ActiveConnection/12")
			}
			if err := store.persist(intent); err != nil {
				t.Fatal(err)
			}
			restarted := newNML3Stager(dir, fixedBootID("boot-a"))
			got, exists, err := restarted.loadIntent()
			if err != nil || !exists || !reflect.DeepEqual(intent, got) {
				t.Fatalf("read-only recovery lost exact stage: %#v exists=%v err=%v", got, exists, err)
			}
		})
	}
}

func TestNML3ReadOnlyIntentRejectsForeignOrBrokenStateWithoutMutation(t *testing.T) {
	cases := []struct {
		name string
		edit func(*nmL3Intent)
	}{
		{"different-UUID", func(i *nmL3Intent) { i.UUID = "foreign" }},
		{"missing-mark", func(i *nmL3Intent) { i.Mark = 0 }},
		{"foreign-owner", func(i *nmL3Intent) { i.Owner = "foreign" }},
		{"invalid-stage", func(i *nmL3Intent) { i.Stage = "active" }},
		{"premature-active-path", func(i *nmL3Intent) { i.ActivePath = "/org/freedesktop/NetworkManager/ActiveConnection/12" }},
		{"missing-IPv4", func(i *nmL3Intent) { i.IPv4 = "" }},
		{"unknown-MTU", func(i *nmL3Intent) { i.MTU = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			session := protectedNMTestSession(t, dir)
			s := newNML3Stager(dir, fixedBootID("boot-a"))
			entry := nmL3Intent{
				Schema: nmL3IntentSchema, Owner: networkSessionStateOwner,
				BootID: "boot-a", SessionID: session.SessionID,
				UUID:      "abcdef00-0000-4000-8000-000000000002",
				Interface: "podlaz0", IPv4: "198.18.0.17/32",
				DNS: "1.1.1.1", MTU: 1500, Metric: 10, Mark: 101, Stage: "planned",
			}
			tc.edit(&entry)
			data, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(s.path(), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, exists, err := s.loadIntent(); err == nil || exists {
				t.Fatalf("invalid intent admitted: exists=%v err=%v", exists, err)
			}
			still, err := os.ReadFile(s.path())
			if err != nil || !reflect.DeepEqual(still, data) {
				t.Fatalf("failed inspection rewrote evidence: %v", err)
			}
		})
	}
}

func TestNML3ReadOnlyIntentRejectsDifferentBootAndSession(t *testing.T) {
	dir := t.TempDir()
	session := protectedNMTestSession(t, dir)
	stager := newNML3Stager(dir, fixedBootID("boot-a"))
	intent := nmL3Intent{
		Schema: nmL3IntentSchema, Owner: networkSessionStateOwner,
		BootID: "boot-a", SessionID: session.SessionID,
		UUID:      "abcdef00-0000-4000-8000-000000000002",
		Interface: "podlaz0", IPv4: "198.18.0.17/32",
		DNS: "1.1.1.1", MTU: 1500, Metric: 10, Mark: 101, Stage: "planned",
	}
	if err := stager.persist(intent); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := newNML3Stager(dir, fixedBootID("boot-b")).loadIntent(); err == nil || exists {
		t.Fatalf("previous-boot L3 intent adopted: exists=%v err=%v", exists, err)
	}
	sessions := newNetworkSessionStateStore(dir, fixedBootID("boot-a"))
	if _, _, err := sessions.Update(func(s *networkSessionState) error {
		s.SessionID = "00112233445566778899aabbccddeeff"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := stager.loadIntent(); err == nil || exists {
		t.Fatalf("foreign Network Session adopted: exists=%v err=%v", exists, err)
	}
}

func TestNML3ReadOnlyIntentDoesNotCleanupPreviousBootSessionState(t *testing.T) {
	dir := t.TempDir()
	session := protectedNMTestSession(t, dir)
	intent := nmL3Intent{
		Schema: nmL3IntentSchema, Owner: networkSessionStateOwner,
		BootID: "boot-a", SessionID: session.SessionID,
		UUID:      "abcdef00-0000-4000-8000-000000000002",
		Interface: "podlaz0", IPv4: "198.18.0.17/32",
		DNS: "1.1.1.1", MTU: 1500, Metric: 10, Mark: 101, Stage: "planned",
	}
	stager := newNML3Stager(dir, fixedBootID("boot-a"))
	if err := stager.persist(intent); err != nil {
		t.Fatal(err)
	}
	store := newNetworkSessionStateStore(dir, fixedBootID("boot-a"))
	if _, _, err := store.Update(func(state *networkSessionState) error {
		state.BootID = "boot-b"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(dir, networkSessionContinuationFileName)
	before, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := stager.loadIntent(); err == nil || ok {
		t.Fatalf("previous-boot session adopted: ok=%v err=%v", ok, err)
	}
	after, err := os.ReadFile(sessionPath)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read-only inspection modified parent Network Session: %v", err)
	}
	if _, err := os.Stat(stager.path()); err != nil {
		t.Fatalf("read-only inspection discarded NM intent: %v", err)
	}
}

func TestNML3ReadOnlyIntentRefusesSymlinksUnknownFieldsAndPermissions(t *testing.T) {
	dir := t.TempDir()
	_ = protectedNMTestSession(t, dir)
	stager := newNML3Stager(dir, fixedBootID("boot-a"))
	if _, exists, err := stager.loadIntent(); err != nil || exists {
		t.Fatalf("missing intent must be distinct from corrupt authority: %v %v", exists, err)
	}
	if err := os.WriteFile(stager.path(), []byte(`{"unknown":"private"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := stager.loadIntent(); err == nil || exists {
		t.Fatalf("unknown fields accepted: %v %v", exists, err)
	}
	if err := os.Chmod(stager.path(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := stager.loadIntent(); err == nil || exists {
		t.Fatalf("world-readable intent accepted: %v %v", exists, err)
	}
	if err := os.Remove(stager.path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/proc/self/status", stager.path()); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := stager.loadIntent(); err == nil || exists {
		t.Fatalf("symlink intent accepted: %v %v", exists, err)
	}
}
