package executor

import (
	"context"
	"errors"
)

const (
	resolvedMissingLinkStderr         = `Failed to resolve interface "podlaz0": No such device`
	resolvedMissingLinkIgnoringStderr = `Failed to resolve interface "podlaz0", ignoring: No such device`
	maxResolvedMissingStderrSize      = 512
)

type processExitCoder interface {
	ExitCode() int
}

func resolvedCommandResultIsMissing(ctx context.Context, result CommandResult, err error) bool {
	if ctx == nil || ctx.Err() != nil {
		return false
	}
	var commandErr commandError
	if !errors.As(err, &commandErr) || commandErr.result != result {
		return false
	}
	if commandErr.name != "resolvectl" || commandErr.parentErr != nil || commandErr.contextErr != nil || commandErr.err == nil {
		return false
	}
	if errors.Is(commandErr.err, context.Canceled) || errors.Is(commandErr.err, context.DeadlineExceeded) {
		return false
	}

	var exitErr processExitCoder
	if !errors.As(commandErr.err, &exitErr) || exitErr.ExitCode() != 1 {
		return false
	}
	if result.ExitCode != 1 || result.RawStdout != "" {
		return false
	}
	if result.RawStderr == "" || len(result.RawStderr) > maxResolvedMissingStderrSize {
		return false
	}
	return exactTerminatedProtocolLine(result.RawStderr, resolvedMissingLinkStderr) ||
		exactTerminatedProtocolLine(result.RawStderr, resolvedMissingLinkIgnoringStderr)
}

// exactTerminatedProtocolLine accepts only the exact protocol payload followed
// by one conventional terminal LF or CRLF. Extra whitespace, missing termination,
// or additional lines are ambiguous and must fail closed.
func exactTerminatedProtocolLine(raw, expected string) bool {
	return raw == expected+"\n" || raw == expected+"\r\n"
}
