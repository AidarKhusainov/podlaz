package executor

import (
	"context"
	"errors"
	"os/exec"
)

const (
	ApplyFailureCauseUnknown            = "unknown"
	ApplyFailureCauseCommandExit        = "command-exit"
	ApplyFailureCauseCommandTimeout     = "command-timeout"
	ApplyFailureCauseCommandUnavailable = "command-unavailable"
)

// ApplyFailureCause returns bounded, privacy-safe typed evidence about why a
// network apply command failed. It deliberately does not inspect human-readable
// stderr and does not assign replay terminality.
func ApplyFailureCause(err error) string {
	var command commandError
	if !errors.As(err, &command) {
		return ApplyFailureCauseUnknown
	}
	if command.parentErr != nil {
		// Parent cancellation/shutdown is lifecycle evidence and must remain
		// authoritative at the replay classifier rather than being reduced to a
		// command failure cause.
		return ApplyFailureCauseUnknown
	}
	if errors.Is(command.contextErr, context.DeadlineExceeded) {
		return ApplyFailureCauseCommandTimeout
	}
	var unavailable *exec.Error
	if errors.As(command.err, &unavailable) {
		return ApplyFailureCauseCommandUnavailable
	}
	if command.result.ExitCode != 0 {
		return ApplyFailureCauseCommandExit
	}
	return ApplyFailureCauseUnknown
}
