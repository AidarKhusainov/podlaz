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
	rawStdout := rawCommandOutput(result.RawStdout, result.Stdout)
	rawStderr := rawCommandOutput(result.RawStderr, result.Stderr)
	if result.ExitCode != 1 || rawStdout != "" {
		return false
	}
	if rawStderr == "" || len(rawStderr) > maxResolvedMissingStderrSize {
		return false
	}
	return exactProtocolLine(rawStderr, resolvedMissingLinkStderr)
}

func rawCommandOutput(raw, normalized string) string {
	if raw != "" {
		return raw
	}
	return normalized
}

// exactProtocolLine accepts only the exact protocol payload and the two
// conventional single-line process endings. Additional whitespace or lines are
// ambiguous and must fail closed.
func exactProtocolLine(raw, expected string) bool {
	return raw == expected || raw == expected+"\n" || raw == expected+"\r\n"
}
