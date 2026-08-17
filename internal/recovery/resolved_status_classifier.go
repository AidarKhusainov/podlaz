package recovery

import "context"

// ResolvedStatusResourceMissingEnvelope classifies the authoritative
// systemd-resolved per-link missing-resource command envelope used by recovery.
// Callers must pass the unmodified command stdout/stderr bytes; malformed or
// unexpected successful stderr remains fail-closed.
func ResolvedStatusResourceMissingEnvelope(ctx context.Context, rawStdout, rawStderr string, exitCode int, err error) bool {
	return resolvedStatusResourceMissing(ctx, CommandResult{
		RawStdout: rawStdout,
		RawStderr: rawStderr,
		ExitCode:  exitCode,
	}, err)
}
