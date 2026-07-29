package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// stopStartedCoreForTransaction proves process quiescence without deleting the
// generated runtime config on an early stop error. The transaction rollback
// owns config removal after host cleanup and process absence both succeed.
func (m *XrayManager) stopStartedCoreForTransaction(cmd *exec.Cmd, done <-chan struct{}) error {
	m.mu.Lock()
	if m.cmd == cmd {
		m.stopping = true
	}
	m.mu.Unlock()
	return m.stopCoreProcessForTransaction(cmd, done)
}

func (m *XrayManager) stopCoreProcessForTransaction(cmd *exec.Cmd, done <-chan struct{}) error {
	if cmd == nil {
		return nil
	}
	if done == nil {
		return errors.New("Xray process quiescence cannot be proven without a completion signal")
	}
	if cmd.Process != nil {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("stop Xray gracefully: %w", err)
		}
	}

	stopTimeout := m.StopTimeout
	if stopTimeout == 0 {
		stopTimeout = defaultStopTimeout
	}
	if waitForCoreCompletion(done, stopTimeout) {
		return nil
	}
	if cmd.Process == nil {
		return errors.New("Xray process quiescence was not confirmed and no process handle is available for force stop")
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("force stop Xray: %w", err)
	}
	if !waitForCoreCompletion(done, stopTimeout) {
		return fmt.Errorf("Xray process quiescence was not confirmed within %s after force stop", stopTimeout)
	}
	return nil
}

func waitForCoreCompletion(done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
