//go:build linux

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	nmGenerationFile = "nm-owned-generation.json"
	nmGenerationLock = "nm-owned-generation.lock"
	nmGenerationSchema = "podlaz.nm-owned-generation.v1"
	nmGenerationMaxSize = 8192
)

// This is a durable deny-until-proven reservation, never an FD admission
// capability. Generation 2 and removal require a future confined supervisor
// with positive proof that the previous cohort cannot retain copied TUN FDs.
type nmGenerationReservation struct {
	Schema string `json:"schema"`
	Owner string `json:"owner"`
	BootID string `json:"boot_id"`
	SessionID string `json:"session_id"`
	Generation uint64 `json:"generation"`
	Nonce string `json:"nonce"`
	State string `json:"state"`
	EgressMark uint32 `json:"egress_mark"`
	Kernel nmTunFDKernelIdentity `json:"kernel"`
	NM nmTunDBusAuthority `json:"network_manager"`
}

type nmGenerationFence struct { session networkSessionStateStore }

func newNMGenerationFence(dir string, bootID bootIDReader) nmGenerationFence {
	return nmGenerationFence{session: newNetworkSessionStateStore(dir, bootID)}
}

func (f nmGenerationFence) path() string { return filepath.Join(f.session.runtimeDir, nmGenerationFile) }

// Reserve is intentionally not wired into the active default TUN lifecycle.
// It does not create a TUN, hand off any descriptor, admit an Xray process,
// mutate NM or authorize network cleanup.
func (f nmGenerationFence) Reserve(ctx context.Context, fd int, sessionID string, mark uint32, kernel nmTunFDKernelIdentity, nm nmTunDBusAuthority) (nmGenerationReservation, error) {
	return f.reserveWith(ctx, fd, sessionID, mark, kernel, nm, verifyNMOwnedTunReadOnlyPreflight)
}

func (f nmGenerationFence) reserveWith(ctx context.Context, fd int, sessionID string, mark uint32, kernel nmTunFDKernelIdentity, nm nmTunDBusAuthority, verify func(context.Context, int, nmTunFDKernelIdentity, nmTunDBusAuthority) error) (nmGenerationReservation, error) {
	if ctx == nil || verify == nil || fd < 3 || mark == 0 || !networkSessionIDPattern.MatchString(sessionID) || kernel.Name == "" || kernel.Name != nm.Name || kernel.MTU <= 0 || uint64(kernel.MTU) != uint64(nm.MTU) {
		return nmGenerationReservation{}, errors.New("incomplete NM generation authority")
	}
	if err := ctx.Err(); err != nil { return nmGenerationReservation{}, err }
	mu := f.session.mutationLock()
	mu.Lock()
	defer mu.Unlock()

	unlock, err := f.lock()
	if err != nil { return nmGenerationReservation{}, err }
	defer unlock()
	if _, exists, err := f.load(); err != nil { return nmGenerationReservation{}, err } else if exists {
		return nmGenerationReservation{}, errors.New("NM generation already reserved; old cohort quiescence unproven")
	}
	session, exists, err := f.session.loadLocked()
	if err != nil { return nmGenerationReservation{}, err }
	bootID, err := f.session.currentBootID()
	if err != nil { return nmGenerationReservation{}, err }
	p := session.Protection
	if !exists || session.BootID != bootID || session.SessionID != sessionID || session.Intent != networkSessionIntentResume ||
		session.Request.Mode != "tun" || session.Replacement != nil || p == nil || p.State != networkSessionProtectionArmed ||
		p.CompositionVersion != privacyEnvelopeMarkedCompositionVersion || p.TunInterface != kernel.Name || len(p.PreviousEgressMarks) != 0 {
		return nmGenerationReservation{}, errors.New("exact armed marked Network Session required before NM generation")
	}
	markArmed := false
	for _, value := range p.EgressMarks { if value == mark { markArmed = true } }
	if !markArmed { return nmGenerationReservation{}, errors.New("NM egress mark not protected by durable Privacy Envelope") }
	if err := verify(ctx, fd, kernel, nm); err != nil { return nmGenerationReservation{}, fmt.Errorf("NM read-only identity proof failed: %w", err) }
	if err := ctx.Err(); err != nil { return nmGenerationReservation{}, err }
	nonce, err := newNetworkSessionID()
	if err != nil { return nmGenerationReservation{}, err }
	entry := nmGenerationReservation{
		Schema: nmGenerationSchema, Owner: networkSessionStateOwner,
		BootID: bootID, SessionID: sessionID, Generation: 1, Nonce: nonce,
		State: "reserved", EgressMark: mark, Kernel: kernel, NM: nm,
	}
	if err := validateNMGenerationReservation(entry); err != nil { return nmGenerationReservation{}, err }
	data, err := json.Marshal(entry)
	if err != nil { return nmGenerationReservation{}, err }
	if err := atomicWritePrivateFile(f.path(), append(data, '\n')); err != nil { return nmGenerationReservation{}, fmt.Errorf("persist NM reservation: %w", err) }
	return entry, nil
}

func validateNMGenerationReservation(e nmGenerationReservation) error {
	if e.Schema != nmGenerationSchema || e.Owner != networkSessionStateOwner || strings.TrimSpace(e.BootID) == "" ||
		!networkSessionIDPattern.MatchString(e.SessionID) || !networkSessionIDPattern.MatchString(e.Nonce) ||
		e.Generation != 1 || e.State != "reserved" || e.EgressMark == 0 ||
		e.Kernel.Name == "" || e.Kernel.Name != e.NM.Name || e.Kernel.Index <= 0 || e.Kernel.MTU <= 0 ||
		e.Kernel.NetNSDevice == 0 || e.Kernel.NetNSInode == 0 || uint64(e.Kernel.MTU) != uint64(e.NM.MTU) ||
		!nmUUIDPattern.MatchString(e.NM.UUID) ||
		!validNMObjectPath(e.NM.DevicePath, "/org/freedesktop/NetworkManager/Devices/") ||
		!validNMObjectPath(e.NM.ActivePath, "/org/freedesktop/NetworkManager/ActiveConnection/") ||
		!validNMObjectPath(e.NM.SettingsPath, "/org/freedesktop/NetworkManager/Settings/") {
		return errors.New("invalid durable NM generation reservation")
	}
	return nil
}

func (f nmGenerationFence) load() (nmGenerationReservation, bool, error) {
	fd, err := unix.Open(f.path(), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) { return nmGenerationReservation{}, false, nil }
	if err != nil { return nmGenerationReservation{}, false, fmt.Errorf("open NM reservation: %w", err) }
	file := os.NewFile(uintptr(fd), f.path())
	defer file.Close()
	info, err := file.Stat()
	if err != nil { return nmGenerationReservation{}, false, err }
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > nmGenerationMaxSize {
		return nmGenerationReservation{}, false, errors.New("unsafe NM reservation file")
	}
	data, err := io.ReadAll(io.LimitReader(file, nmGenerationMaxSize+1))
	if err != nil { return nmGenerationReservation{}, false, err }
	if len(data) > nmGenerationMaxSize { return nmGenerationReservation{}, false, errors.New("oversized NM reservation") }
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var entry nmGenerationReservation
	if err := decoder.Decode(&entry); err != nil { return nmGenerationReservation{}, false, fmt.Errorf("decode NM reservation: %w", err) }
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nmGenerationReservation{}, false, errors.New("trailing NM reservation data")
	}
	if err := validateNMGenerationReservation(entry); err != nil { return nmGenerationReservation{}, false, err }
	bootID, err := f.session.currentBootID()
	if err != nil { return nmGenerationReservation{}, false, err }
	if entry.BootID != bootID {
		return nmGenerationReservation{}, false, errors.New("old-boot NM generation cannot be adopted or removed")
	}
	return entry, true, nil
}

func (f nmGenerationFence) lock() (func(), error) {
	if err := os.MkdirAll(f.session.runtimeDir, 0o700); err != nil { return nil, err }
	fd, err := unix.Open(filepath.Join(f.session.runtimeDir, nmGenerationLock), unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil { return nil, fmt.Errorf("open NM generation lock: %w", err) }
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 || stat.Nlink != 1 {
		unix.Close(fd)
		return nil, errors.New("unsafe NM generation lock")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("NM generation is concurrently reserved: %w", err)
	}
	return func() { unix.Flock(fd, unix.LOCK_UN); unix.Close(fd) }, nil
}
