package recovery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

const (
	managedDNSCandidateKind             = "dns-link"
	managedDNSDescription               = "systemd-resolved link state"
	managedDNSTarget                    = "systemd-resolved link " + managedInterface
	maxResolvedMissingStderrSize        = 512
	resolvedMissingDeviceStderr         = `Failed to resolve interface "podlaz0": No such device`
	resolvedMissingDeviceIgnoringStderr = `Failed to resolve interface "podlaz0", ignoring: No such device`
	resolvedRouteOnlyDomain             = "~."
	resolvedDefaultRouteProtocol        = "+DefaultRoute"
)

type resolvedLinkObservation uint8

const (
	resolvedLinkUnknown resolvedLinkObservation = iota
	resolvedLinkPresent
	resolvedLinkAbsent
)

func (r *ScanResult) scanManagedResolvedLink(ctx context.Context, runner CommandRunner) {
	resolvectlPath, err := runner.LookPath("resolvectl")
	if err != nil {
		r.Warnings = append(r.Warnings, Warning{Target: managedDNSTarget, Message: "resolvectl command is unavailable; resolved link state is unknown"})
		return
	}
	status, statusErr := runCommand(ctx, runner, resolvectlPath, "status", managedInterface, "--no-pager")
	switch observeResolvedLink(ctx, status, statusErr) {
	case resolvedLinkAbsent:
		return
	case resolvedLinkUnknown:
		r.Warnings = append(r.Warnings, Warning{Target: managedDNSTarget, Message: resolvedObservationFailureMessage(status, statusErr)})
		return
	case resolvedLinkPresent:
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
	switch observeResolvedLink(ctx, status, statusErr) {
	case resolvedLinkAbsent:
		return recovered(candidate)
	case resolvedLinkPresent:
		return skipped(candidate, "systemd-resolved link configuration persisted after revert; restart systemd-resolved manually")
	default:
		return failed(candidate, fmt.Errorf("verify systemd-resolved cleanup: %s", resolvedObservationFailureMessage(status, statusErr)))
	}
}

// observeResolvedLink classifies actionable per-link systemd-resolved state,
// not merely the lifetime of resolved's transient Link record. A successful
// status command may briefly retain an empty record after revert/link removal;
// that record contains no DNS mutation to recover and is therefore absent.
// Ambiguous, malformed, or non-podlaz concrete DNS state remains unknown.
func observeResolvedLink(ctx context.Context, result CommandResult, err error) resolvedLinkObservation {
	switch {
	case resolvedStatusResourceMissing(ctx, result, err):
		return resolvedLinkAbsent
	case !commandSucceeded(result, err):
		return resolvedLinkUnknown
	}

	links := netsnapshot.ParseResolvedLinks(result.Stdout)
	matches := make([]netsnapshot.ResolvedLink, 0, 1)
	for _, link := range links {
		if link.Name == managedInterface {
			matches = append(matches, link)
		}
	}
	if len(matches) != 1 {
		return resolvedLinkUnknown
	}
	link := matches[0]
	if resolvedLinkHasPodlazConfiguration(link) {
		return resolvedLinkPresent
	}
	if resolvedLinkHasConcreteDNSConfiguration(link) {
		return resolvedLinkUnknown
	}
	return resolvedLinkAbsent
}

func resolvedLinkHasPodlazConfiguration(link netsnapshot.ResolvedLink) bool {
	return containsResolvedValue(link.DNSDomains, resolvedRouteOnlyDomain) &&
		containsResolvedValue(link.Protocols, resolvedDefaultRouteProtocol) &&
		(strings.TrimSpace(link.CurrentDNSServer) != "" || len(link.DNSServers) > 0)
}

func resolvedLinkHasConcreteDNSConfiguration(link netsnapshot.ResolvedLink) bool {
	return strings.TrimSpace(link.CurrentDNSServer) != "" ||
		len(link.DNSServers) > 0 ||
		len(link.DNSDomains) > 0 ||
		containsResolvedValue(link.Protocols, resolvedDefaultRouteProtocol)
}

func containsResolvedValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func resolvedObservationFailureMessage(result CommandResult, err error) string {
	if commandSucceeded(result, err) {
		return "systemd-resolved link status is ambiguous or contains non-podlaz DNS configuration"
	}
	return commandFailureMessage(result, err)
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
	return exactTerminatedRecoveryProtocolLine(result.RawStderr, resolvedMissingDeviceStderr) ||
		exactTerminatedRecoveryProtocolLine(result.RawStderr, resolvedMissingDeviceIgnoringStderr)
}

func resolvedStatusResourceMissing(ctx context.Context, result CommandResult, err error) bool {
	if ctx == nil || ctx.Err() != nil || err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var exitErr interface{ ExitCode() int }
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || result.ExitCode != 1 || result.RawStdout != "" {
		return false
	}
	if result.RawStderr == "" || len(result.RawStderr) > maxResolvedMissingStderrSize {
		return false
	}
	return exactTerminatedRecoveryProtocolLine(result.RawStderr, `Link podlaz0 does not exist.`) ||
		exactTerminatedRecoveryProtocolLine(result.RawStderr, resolvedMissingDeviceStderr) ||
		exactTerminatedRecoveryProtocolLine(result.RawStderr, resolvedMissingDeviceIgnoringStderr)
}

// exactTerminatedRecoveryProtocolLine accepts only the exact protocol payload
// followed by one terminal LF or CRLF. Extra whitespace, missing termination, or
// additional lines fail closed.
func exactTerminatedRecoveryProtocolLine(raw, expected string) bool {
	return raw == expected+"\n" || raw == expected+"\r\n"
}
