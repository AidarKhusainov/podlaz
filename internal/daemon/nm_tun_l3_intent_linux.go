//go:build linux

package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

const nmL3MaxIntentBytes = 8192

// loadIntent reads the exact persisted NM L3 mutation intent without
// contacting or modifying NetworkManager. It is not cleanup authority.
// Unknown/foreign/previous-boot state is retained but cannot be adopted.
func (s nmL3Stager) loadIntent() (nmL3Intent, bool, error) {
	fd, err := unix.Open(s.path(), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nmL3Intent{}, false, nil
	}
	if err != nil {
		return nmL3Intent{}, false, fmt.Errorf("open NM L3 intent safely: %w", err)
	}
	file := os.NewFile(uintptr(fd), s.path())
	defer file.Close()

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nmL3Intent{}, false, fmt.Errorf("inspect NM L3 intent file: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 ||
		stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) ||
		stat.Size <= 0 || stat.Size > nmL3MaxIntentBytes {
		return nmL3Intent{}, false, errors.New("untrusted NM L3 intent file identity or size")
	}

	data, err := io.ReadAll(io.LimitReader(file, nmL3MaxIntentBytes+1))
	if err != nil {
		return nmL3Intent{}, false, fmt.Errorf("read NM L3 intent: %w", err)
	}
	if len(data) > nmL3MaxIntentBytes {
		return nmL3Intent{}, false, errors.New("NM L3 intent exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var intent nmL3Intent
	if err := decoder.Decode(&intent); err != nil {
		return nmL3Intent{}, false, fmt.Errorf("decode NM L3 intent: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nmL3Intent{}, false, errors.New("NM L3 intent has trailing content")
	}
	if err := validateNML3Intent(intent); err != nil {
		return nmL3Intent{}, false, err
	}

	boot, err := s.session.currentBootID()
	if err != nil {
		return nmL3Intent{}, false, err
	}
	if intent.BootID != boot {
		return nmL3Intent{}, false, errors.New("NM L3 intent belongs to a different boot")
	}
	// Read the parent Network Session without invoking Load(): its ordinary
	// startup recovery may remove stale boot state, which is forbidden for a
	// read-only NM inspection. Invalid or missing evidence stays untouched.
	session, err := s.readExactSessionWithoutMutation()
	if err != nil {
		return nmL3Intent{}, false, err
	}
	if session.BootID != intent.BootID || session.SessionID != intent.SessionID {
		return nmL3Intent{}, false, errors.New("NM L3 intent has no matching Network Session")
	}
	return intent, true, nil
}

func validateNML3Intent(intent nmL3Intent) error {
	if intent.Schema != nmL3IntentSchema || intent.Owner != networkSessionStateOwner ||
		strings.TrimSpace(intent.BootID) == "" ||
		!networkSessionIDPattern.MatchString(intent.SessionID) ||
		!nmUUIDPattern.MatchString(intent.UUID) || intent.Mark == 0 ||
		validateNML3Plan(nmL3Plan{
			Interface: intent.Interface, MTU: intent.MTU,
			IPv4: intent.IPv4, DNS: intent.DNS, Metric: intent.Metric,
		}) != nil {
		return errors.New("invalid durable NM L3 intent")
	}
	switch intent.Stage {
	case "planned":
		if intent.SettingsPath != "" || intent.ActivePath != "" {
			return errors.New("NM planned intent cannot claim D-Bus object paths")
		}
	case "profile-added":
		if !validNMObjectPath(intent.SettingsPath, "/org/freedesktop/NetworkManager/Settings/") ||
			intent.ActivePath != "" {
			return errors.New("NM profile-added intent has invalid object paths")
		}
	case "activating":
		if !validNMObjectPath(intent.SettingsPath, "/org/freedesktop/NetworkManager/Settings/") ||
			!validNMObjectPath(intent.ActivePath, "/org/freedesktop/NetworkManager/ActiveConnection/") {
			return errors.New("NM activating intent has invalid object paths")
		}
	default:
		return errors.New("unknown NM L3 intent stage")
	}
	return nil
}

// readExactSessionWithoutMutation deliberately does not use the normal
// Network Session loader: legacy migration and previous-boot reconciliation
// may have filesystem effects, which inspection must not perform.
func (s nmL3Stager) readExactSessionWithoutMutation() (networkSessionState, error) {
	path := filepath.Join(s.session.runtimeDir, networkSessionContinuationFileName)
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return networkSessionState{}, fmt.Errorf("read NM parent session authority: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return networkSessionState{}, fmt.Errorf("inspect NM parent session authority: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 ||
		stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 ||
		stat.Size <= 0 || stat.Size > maxNetworkSessionStateBytes {
		return networkSessionState{}, errors.New("invalid NM parent session file identity")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxNetworkSessionStateBytes+1))
	if err != nil {
		return networkSessionState{}, err
	}
	if len(data) > maxNetworkSessionStateBytes {
		return networkSessionState{}, errors.New("NM parent session exceeds size limit")
	}
	var session networkSessionState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&session); err != nil {
		return networkSessionState{}, fmt.Errorf("decode NM parent session authority: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return networkSessionState{}, errors.New("NM parent session has trailing data")
	}
	if err := validateNetworkSessionState(session); err != nil {
		return networkSessionState{}, err
	}
	return session, nil
}
