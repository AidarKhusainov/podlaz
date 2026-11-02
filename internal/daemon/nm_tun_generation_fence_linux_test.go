//go:build linux

package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func protectedNMTestSession(t *testing.T, dir string) networkSessionState {
	t.Helper()
	store := newNetworkSessionStateStore(dir, fixedBootID("boot-a"))
	session, err := store.BeginOrResume(testContinuationRequest())
	if err != nil { t.Fatal(err) }
	p := testNetworkSessionProtection()
	p.CompositionVersion = privacyEnvelopeMarkedCompositionVersion
	p.BootstrapIPv4 = nil
	p.EgressMarks = []uint32{101}
	if err := store.SetProtection(&p); err != nil { t.Fatal(err) }
	return session
}

func TestNMGenerationReservationPersistsBeforeAdmissionAndBlocksReplay(t *testing.T) {
	dir := t.TempDir()
	session := protectedNMTestSession(t, dir)
	f := newNMGenerationFence(dir, fixedBootID("boot-a"))
	k, nm := testNMFDIdentity(), validExpectedNMTun()
	calls := 0
	proof := func(_ context.Context, fd int, gotK nmTunFDKernelIdentity, gotNM nmTunDBusAuthority) error {
		calls++
		if fd != 9 || gotK != k || gotNM != nm { t.Fatal("unexpected identity") }
		return nil
	}
	entry, err := f.reserveWith(context.Background(), 9, session.SessionID, 101, k, nm, proof)
	if err != nil { t.Fatal(err) }
	if entry.Generation != 1 || entry.State != "reserved" || entry.Nonce == "" || calls != 1 { t.Fatalf("bad reservation: %#v calls=%d", entry, calls) }
	info, err := os.Stat(f.path())
	if err != nil || info.Mode().Perm() != 0o600 { t.Fatalf("reservation not private: %v %v", info, err) }
	restarted := newNMGenerationFence(dir, fixedBootID("boot-a"))
	loaded, found, err := restarted.load()
	if err != nil || !found || !reflect.DeepEqual(loaded, entry) { t.Fatalf("reservation lost on restart: %#v %v %v", loaded, found, err) }
	if _, err := restarted.reserveWith(context.Background(), 9, session.SessionID, 101, k, nm, proof); err == nil || calls != 1 {
		t.Fatalf("duplicate reservation admitted: err=%v calls=%d", err, calls)
	}
}

func TestNMGenerationReservationRejectsUnarmedAndForeignStateBeforeProof(t *testing.T) {
	for _, tc := range []struct{
		name string
		edit func(*networkSessionState)
		mark uint32
	}{
		{"unarmed", func(s *networkSessionState) { s.Protection.State = networkSessionProtectionUnarmed }, 101},
		{"no-protection", func(s *networkSessionState) { s.Protection = nil }, 101},
		{"terminal", func(s *networkSessionState) { s.Intent = networkSessionIntentTerminal }, 101},
		{"foreign-mark", func(*networkSessionState) {}, 102},
		{"other-session", func(s *networkSessionState) { s.SessionID = "00112233445566778899aabbccddeeff" }, 101},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			session := protectedNMTestSession(t, dir)
			store := newNetworkSessionStateStore(dir, fixedBootID("boot-a"))
			if _, _, err := store.Update(func(s *networkSessionState) error { tc.edit(s); return nil }); err != nil { t.Fatal(err) }
			called := false
			f := newNMGenerationFence(dir, fixedBootID("boot-a"))
			_, err := f.reserveWith(context.Background(), 9, session.SessionID, tc.mark, testNMFDIdentity(), validExpectedNMTun(),
				func(context.Context, int, nmTunFDKernelIdentity, nmTunDBusAuthority) error { called = true; return nil })
			if err == nil || called { t.Fatalf("unprotected admission: err=%v called=%v", err, called) }
			if _, err := os.Stat(f.path()); !errors.Is(err, os.ErrNotExist) { t.Fatalf("authority unexpectedly persisted: %v", err) }
		})
	}
}

func TestNMGenerationReservationRejectsFailedProofAndCorruptAuthority(t *testing.T) {
	dir := t.TempDir()
	session := protectedNMTestSession(t, dir)
	f := newNMGenerationFence(dir, fixedBootID("boot-a"))
	cause := errors.New("foreign NM connection")
	k, nm := testNMFDIdentity(), validExpectedNMTun()
	if _, err := f.reserveWith(context.Background(), 9, session.SessionID, 101, k, nm,
		func(context.Context, int, nmTunFDKernelIdentity, nmTunDBusAuthority) error { return cause }); !errors.Is(err, cause) { t.Fatalf("lost proof failure: %v", err) }
	if _, err := os.Stat(f.path()); !errors.Is(err, os.ErrNotExist) { t.Fatalf("failed proof left reservation: %v", err) }
	if _, err := f.reserveWith(context.Background(), 9, session.SessionID, 101, k, nm,
		func(context.Context, int, nmTunFDKernelIdentity, nmTunDBusAuthority) error { return nil }); err != nil { t.Fatal(err) }
	if _, found, err := newNMGenerationFence(dir, fixedBootID("boot-b")).load(); err == nil || found { t.Fatalf("old-boot authority admitted: %v %v", found, err) }
	bad := []byte(`{"schema":"foreign","private":"raw"}`)
	if err := os.WriteFile(f.path(), bad, 0o600); err != nil { t.Fatal(err) }
	if _, _, err := f.load(); err == nil { t.Fatal("corrupt authority admitted") }
	current, err := os.ReadFile(f.path())
	if err != nil || !reflect.DeepEqual(current, bad) { t.Fatalf("corrupt authority was erased: %v", err) }
	if err := os.Remove(f.path()); err != nil { t.Fatal(err) }
	if err := os.Symlink(filepath.Join(dir, "foreign"), f.path()); err != nil { t.Fatal(err) }
	if _, _, err := f.load(); err == nil { t.Fatal("symlink authority was followed") }
}

func TestNMGenerationReservationRespectsCancellation(t *testing.T) {
	dir := t.TempDir()
	session := protectedNMTestSession(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	_, err := newNMGenerationFence(dir, fixedBootID("boot-a")).reserveWith(ctx, 9, session.SessionID, 101,
		testNMFDIdentity(), validExpectedNMTun(), func(context.Context, int, nmTunFDKernelIdentity, nmTunDBusAuthority) error {
			called = true
			return nil
		})
	if !errors.Is(err, context.Canceled) || called { t.Fatalf("cancelled reservation proceeded: err=%v, called=%v", err, called) }
}
