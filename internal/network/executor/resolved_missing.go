package executor

import (
	"context"
	"errors"
)

const (
	resolvedMissingLinkStderr    = `Failed to resolve interface "podlaz0": No such device`
	maxResolvedMissingStderrSize = 512
)

type processExitCoder interface {
	ExitCode() int
}

func resolvedCommandResultIsMissing(ctx context.Context, result CommandResult, err error) bool {
	if ctx == nil || ctx.Err() != nil || err == nil {
		return false
	}

	var commandErr commandError
	if !errors.As(err, &commandErr) {
		return false
	}
	if commandErr.contextErr != nil || commandErr.err == nil {
		return false
	}
	if commandErr.result != result {
		return false
	}
	if errors.Is(commandErr.err, context.Canceled) || errors.Is(commandErr.err, context.DeadlineExceeded) {
		return false
	}

	var exitErr processExitCoder
	if !errors.As(commandErr.err, &exitErr) || exitErr.ExitCode() != 1 {
		return false
	}
	if result.ExitCode != 1 || result.Stdout != "" {
		return false
	}
	if result.Stderr == "" || len(result.Stderr) > maxResolvedMissingStderrSize {
		return false
	}
	return result.Stderr == resolvedMissingLinkStderr
}
