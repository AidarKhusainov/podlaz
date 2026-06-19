package daemon

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func stopRollbackChildProcesses(tx txstate.Transaction) error {
	var errs []error
	for _, child := range tx.Rollback.ChildProcesses {
		if child.PID <= 0 {
			continue
		}
		matched, err := rollbackChildProcessMatches(child)
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
		}
	}
	return errors.Join(errs...)
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
	case "tun2socks":
		return strings.Contains(cmdline, "tun2socks"), nil
	default:
		return false, nil
	}
}
