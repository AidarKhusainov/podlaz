package recovery

import (
	"context"
	"errors"
)

// ResolvedStatusResourceMissingEnvelope classifies the authoritative
// systemd-resolved per-link missing-resource command envelope used by recovery.
// Callers should pass unmodified command stdout/stderr bytes. When an injected
// legacy runner cannot provide raw bytes, only the pre-existing exact exit-1
// missing-link forms are accepted; successful stderr always requires the strict
// raw-byte protocol so exit-0 cannot be broadened by normalization.
func ResolvedStatusResourceMissingEnvelope(ctx context.Context, stdout, stderr, rawStdout, rawStderr string, exitCode int, err error) bool {
	result := CommandResult{
		Stdout:    stdout,
		Stderr:    stderr,
		RawStdout: rawStdout,
		RawStderr: rawStderr,
		ExitCode:  exitCode,
	}
	if rawStdout != "" || rawStderr != "" {
		return resolvedStatusResourceMissing(ctx, result, err)
	}
	return legacyResolvedStatusResourceMissing(ctx, result, err)
}

func legacyResolvedStatusResourceMissing(ctx context.Context, result CommandResult, err error) bool {
	if ctx == nil || ctx.Err() != nil || err == nil ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		result.ExitCode != 1 || result.Stdout != "" || len(result.Stderr) > maxResolvedMissingStderrSize {
		return false
	}
	switch result.Stderr {
	case `Link podlaz0 does not exist.`, resolvedMissingDeviceStderr, resolvedMissingDeviceIgnoringStderr:
		return true
	default:
		return false
	}
}
