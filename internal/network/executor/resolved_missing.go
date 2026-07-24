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
	stdout, stderr, raw := commandProtocolOutput(result)
	if result.ExitCode != 1 || stdout != "" {
		return false
	}
	if stderr == "" || len(stderr) > maxResolvedMissingStderrSize {
		return false
	}
	if raw {
		return exactTerminatedProtocolLine(stderr, resolvedMissingLinkStderr)
	}
	return stderr == resolvedMissingLinkStderr
}

func commandProtocolOutput(result CommandResult) (stdout, stderr string, raw bool) {
	if result.RawStdout != "" || result.RawStderr != "" {
		return result.RawStdout, result.RawStderr, true
	}
	return result.Stdout, result.Stderr, false
}

// exactTerminatedProtocolLine accepts only the exact protocol payload followed
// by one conventional terminal LF or CRLF. Extra whitespace, missing termination,
// or additional lines are ambiguous and must fail closed.
func exactTerminatedProtocolLine(raw, expected string) bool {
	return raw == expected+"\n" || raw == expected+"\r\n"
}
