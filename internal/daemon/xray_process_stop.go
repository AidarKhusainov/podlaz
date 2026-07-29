package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// stopCoreProcessBounded is the single process-supervision contract for every
// daemon lifecycle path. Callers own policy decisions such as whether runtime
// config or transaction metadata may be removed after this function returns.
func stopCoreProcessBounded(cmd *exec.Cmd, done <-chan struct{}, timeout time.Duration) error {
	if cmd == nil {
		return nil
	}
	if done == nil {
		return errors.New("Xray process quiescence cannot be proven without a completion signal")
	}
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}
	if coreCompletionObserved(done) {
		return nil
	}

	if cmd.Process != nil {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("stop Xray gracefully: %w", err)
		}
	}
	if waitForCoreCompletion(done, timeout) {
		return nil
	}
	if cmd.Process == nil {
		return errors.New("Xray process quiescence was not confirmed and no process handle is available for force stop")
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("force stop Xray: %w", err)
	}
	if !waitForCoreCompletion(done, timeout) {
		return fmt.Errorf("Xray process quiescence was not confirmed within %s after force stop", timeout)
	}
	return nil
}

func coreCompletionObserved(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
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
