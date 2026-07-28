package daemon

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

const rollbackChildProcessPollInterval = 25 * time.Millisecond

var (
	stopRollbackChildProcesses    = defaultStopRollbackChildProcesses
	rollbackChildProcessStopLimit = defaultStopTimeout
)

type rollbackChildIdentity struct {
	PID       int
	StartTime string
}

func defaultStopRollbackChildProcesses(tx txstate.Transaction) error {
	var errs []error
	for _, child := range tx.Rollback.ChildProcesses {
		if child.PID <= 0 {
			continue
		}
		identity, matched, err := inspectRollbackChildProcess(child)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			errs = append(errs, err)
			continue
		}
		if !matched {
			continue
		}
		process, err := os.FindProcess(child.PID)
		if err != nil {
			errs = append(errs, fmt.Errorf("find child process %d (%s): %w", child.PID, child.Label, err))
			continue
		}
		if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
			errs = append(errs, fmt.Errorf("stop child process %d (%s): %w", child.PID, child.Label, err))
			continue
		}
		stopped, err := waitForRollbackChildAbsence(child, identity, rollbackChildProcessStopLimit)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if stopped {
			continue
		}

		// Revalidate the exact process identity immediately before escalation so
		// PID reuse can never authorize SIGKILL against a different process.
		stillOwned, err := rollbackChildIdentityStillMatches(child, identity)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !stillOwned {
			continue
		}
		if err := process.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
			errs = append(errs, fmt.Errorf("force stop child process %d (%s): %w", child.PID, child.Label, err))
			continue
		}
		stopped, err = waitForRollbackChildAbsence(child, identity, rollbackChildProcessStopLimit)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !stopped {
			errs = append(errs, fmt.Errorf("child process %d (%s) remained present after SIGKILL", child.PID, child.Label))
		}
	}
	return errors.Join(errs...)
}

func waitForRollbackChildAbsence(child txstate.ChildProcessRollback, identity rollbackChildIdentity, limit time.Duration) (bool, error) {
	if limit <= 0 {
		limit = defaultStopTimeout
	}
	deadline := time.Now().Add(limit)
	for {
		matched, err := rollbackChildIdentityStillMatches(child, identity)
		if err != nil {
			return false, err
		}
		if !matched {
			return true, nil
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		time.Sleep(rollbackChildProcessPollInterval)
	}
}

func rollbackChildIdentityStillMatches(child txstate.ChildProcessRollback, identity rollbackChildIdentity) (bool, error) {
	current, matched, err := inspectRollbackChildProcess(child)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return matched && current == identity, nil
}

func inspectRollbackChildProcess(child txstate.ChildProcessRollback) (rollbackChildIdentity, bool, error) {
	matched, err := rollbackChildProcessMatches(child)
	if err != nil || !matched {
		return rollbackChildIdentity{}, matched, err
	}
	startTime, err := rollbackChildProcessStartTime(child.PID)
	if err != nil {
		return rollbackChildIdentity{}, false, err
	}
	return rollbackChildIdentity{PID: child.PID, StartTime: startTime}, true, nil
}

func rollbackChildProcessMatches(child txstate.ChildProcessRollback) (bool, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", child.PID))
	if err != nil {
		return false, err
	}
	cmdline := strings.ReplaceAll(string(data), "\x00", " ")
	switch strings.TrimSpace(child.Label) {
	case "xray":
		return strings.Contains(cmdline, "xray") && (child.ConfigRef == "" || strings.Contains(cmdline, child.ConfigRef)), nil
	default:
		return false, nil
	}
}

func rollbackChildProcessStartTime(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	text := string(data)
	closeParen := strings.LastIndex(text, ")")
	if closeParen < 0 || closeParen+2 >= len(text) {
		return "", fmt.Errorf("parse child process %d stat: malformed comm field", pid)
	}
	fields := strings.Fields(text[closeParen+2:])
	// fields[0] is stat field 3 (state); starttime is stat field 22.
	const startTimeIndex = 22 - 3
	if len(fields) <= startTimeIndex {
		return "", fmt.Errorf("parse child process %d stat: missing start time", pid)
	}
	return fields[startTimeIndex], nil
}
