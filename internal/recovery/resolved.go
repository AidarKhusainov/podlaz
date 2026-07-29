package recovery

import (
	"context"
	"errors"
	"fmt"
)

const (
	managedDNSCandidateKind      = "dns-link"
	managedDNSDescription        = "systemd-resolved link state"
	managedDNSTarget             = "systemd-resolved link " + managedInterface
	maxResolvedMissingStderrSize = 512
	resolvedMissingDeviceStderr  = `Failed to resolve interface "podlaz0": No such device`
)

func (r *ScanResult) scanManagedResolvedLink(ctx context.Context, runner CommandRunner) {
	resolvectlPath, err := runner.LookPath("resolvectl")
	if err != nil {
		return
	}
	status, statusErr := runCommand(ctx, runner, resolvectlPath, "status", managedInterface, "--no-pager")
	if resolvedStatusResourceMissing(status) {
		return
	}
	if !commandSucceeded(status, statusErr) {
		r.Warnings = append(r.Warnings, Warning{Target: managedDNSTarget, Message: commandFailureMessage(status, statusErr)})
		return
	}

	ipPath, err := runner.LookPath("ip")
	if err != nil {
		r.Warnings = append(r.Warnings, Warning{Target: managedDNSTarget, Message: "ip command is unavailable; cannot prove the resolved link is stale"})
		return
	}
	link, linkErr := runCommand(ctx, runner, ipPath, "link", "show", "dev", managedInterface)
	switch {
	case commandSucceeded(link, linkErr):
		return
	case resourceMissing(link):
		r.Candidates = append(r.Candidates, Candidate{Kind: managedDNSCandidateKind, Description: managedDNSDescription, Target: managedInterface})
	case commandFailedUnexpectedly(link, linkErr):
		r.Warnings = append(r.Warnings, Warning{Target: managedDNSTarget, Message: "cannot verify interface absence: " + commandFailureMessage(link, linkErr)})
	}
}

func (e OSCleanupExecutor) cleanupManagedResolvedLink(ctx context.Context, candidate Candidate) CleanupResult {
	if candidate.Target != managedInterface {
		return skipped(candidate, "non-podlaz DNS link target")
	}
	ipPath, err := e.Runner.LookPath("ip")
	if err != nil {
		return skipped(candidate, "ip command is unavailable; cannot prove the resolved link is stale")
	}
	link, linkErr := runCommand(ctx, e.Runner, ipPath, "link", "show", "dev", managedInterface)
	switch {
	case commandSucceeded(link, linkErr):
		return skipped(candidate, "interface still exists; refusing stale DNS cleanup for a live link")
	case resourceMissing(link):
	case commandFailedUnexpectedly(link, linkErr):
		return failed(candidate, fmt.Errorf("verify interface absence: %s", commandFailureMessage(link, linkErr)))
	}

	resolvectlPath, err := e.Runner.LookPath("resolvectl")
	if err != nil {
		return failed(candidate, fmt.Errorf("resolvectl command is unavailable"))
	}
	revert, revertErr := runCommand(ctx, e.Runner, resolvectlPath, "revert", managedInterface)
	if resolvedMissingDeviceResult(ctx, revert, revertErr) {
		return recovered(candidate)
	}
	if !commandSucceeded(revert, revertErr) {
		return failed(candidate, fmt.Errorf("revert systemd-resolved DNS: %s", commandFailureMessage(revert, revertErr)))
	}

	status, statusErr := runCommand(ctx, e.Runner, resolvectlPath, "status", managedInterface, "--no-pager")
	if resolvedStatusResourceMissing(status) {
		return recovered(candidate)
	}
	if commandSucceeded(status, statusErr) {
		return skipped(candidate, "systemd-resolved link record persisted after revert; restart systemd-resolved manually")
	}
	return failed(candidate, fmt.Errorf("verify systemd-resolved cleanup: %s", commandFailureMessage(status, statusErr)))
}

func resolvedMissingDeviceResult(ctx context.Context, result CommandResult, err error) bool {
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var exitErr interface{ ExitCode() int }
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || result.ExitCode != 1 {
		return false
	}
	if result.RawStdout != "" {
		return false
	}
	if result.RawStderr == "" || len(result.RawStderr) > maxResolvedMissingStderrSize {
		return false
	}
	return exactTerminatedRecoveryProtocolLine(result.RawStderr, resolvedMissingDeviceStderr)
}

func resolvedStatusResourceMissing(result CommandResult) bool {
	if result.ExitCode != 1 || result.RawStdout != "" {
		return false
	}
	if result.RawStderr == "" || len(result.RawStderr) > maxResolvedMissingStderrSize {
		return false
	}
	return exactTerminatedRecoveryProtocolLine(result.RawStderr, `Link podlaz0 does not exist.`) || exactTerminatedRecoveryProtocolLine(result.RawStderr, resolvedMissingDeviceStderr)
}

// exactTerminatedRecoveryProtocolLine accepts only the exact protocol payload
// followed by one terminal LF or CRLF. Extra whitespace, missing termination, or
// additional lines fail closed.
func exactTerminatedRecoveryProtocolLine(raw, expected string) bool {
	return raw == expected+"\n" || raw == expected+"\r\n"
}
