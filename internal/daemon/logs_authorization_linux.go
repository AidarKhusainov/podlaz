//go:build linux

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func authorizeDaemonLogsPeer(subject PeerSubject) error {
	if subject.UID == 0 {
		return nil
	}
	if subject.PID <= 0 || subject.StartTime == 0 {
		return fmt.Errorf("%w: daemon could not establish the local logs peer identity", ErrAuthorizationUnavailable)
	}

	daemonGID := uint32(os.Getegid())
	if subject.GID == daemonGID {
		return nil
	}

	startBefore, err := readPeerProcessStartTime(subject.PID)
	if err != nil || startBefore != subject.StartTime {
		return fmt.Errorf("%w: daemon could not verify the local logs peer process", ErrAuthorizationUnavailable)
	}
	statusPath := filepath.Join(string(os.PathSeparator)+"proc", strconv.Itoa(subject.PID), "status")
	status, err := os.ReadFile(statusPath)
	if err != nil {
		return fmt.Errorf("%w: daemon could not read the local logs peer groups", ErrAuthorizationUnavailable)
	}
	member, err := processStatusContainsGroup(status, daemonGID)
	if err != nil {
		return fmt.Errorf("%w: daemon could not verify the local logs peer groups", ErrAuthorizationUnavailable)
	}
	startAfter, err := readPeerProcessStartTime(subject.PID)
	if err != nil || startAfter != subject.StartTime {
		return fmt.Errorf("%w: daemon could not verify the local logs peer process", ErrAuthorizationUnavailable)
	}
	if !member {
		return fmt.Errorf("%w: daemon logs require membership in the podlaz daemon access group", ErrAuthorizationDenied)
	}
	return nil
}

func processStatusContainsGroup(status []byte, target uint32) (bool, error) {
	for _, line := range strings.Split(string(status), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "Groups" {
			continue
		}
		for _, field := range strings.Fields(value) {
			group, err := strconv.ParseUint(field, 10, 32)
			if err != nil {
				return false, fmt.Errorf("malformed supplementary group")
			}
			if uint32(group) == target {
				return true, nil
			}
		}
		return false, nil
	}
	return false, fmt.Errorf("missing supplementary groups")
}
