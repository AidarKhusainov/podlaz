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
	resolvedNoDefaultRouteProtocol      = "-DefaultRoute"
)

type resolvedLinkObservation uint8

const (
	resolvedLinkUnknown resolvedLinkObservation = iota
	resolvedLinkPresent
	resolvedLinkAbsent
)

type resolvedDefaultRoutePolarity uint8

const (
	resolvedDefaultRouteUnknown resolvedDefaultRoutePolarity = iota
	resolvedDefaultRoutePositive
	resolvedDefaultRouteNegative
	resolvedDefaultRouteConflicting
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
// Recovery authority requires a live caller context, a clean command envelope,
// and a strict target-section parse: cancellation/deadline, stderr, malformed,
// partial, duplicate, or unsupported output remains unknown rather than being
// downgraded to absence.
func observeResolvedLink(ctx context.Context, result CommandResult, err error) resolvedLinkObservation {
	if ctx != nil && ctx.Err() != nil {
		return resolvedLinkUnknown
	}
	switch {
	case resolvedStatusResourceMissing(ctx, result, err):
		return resolvedLinkAbsent
	case !resolvedStatusSucceededWithoutStderr(result, err):
		return resolvedLinkUnknown
	}

	link, ok := parseManagedResolvedLinkStatus(result.Stdout)
	if !ok {
		return resolvedLinkUnknown
	}
	if resolvedLinkHasPodlazConfiguration(link) {
		return resolvedLinkPresent
	}
	if resolvedLinkHasConcreteDNSConfiguration(link) {
		return resolvedLinkUnknown
	}
	if resolvedLinkIsProvenEmptyTransient(link) {
		return resolvedLinkAbsent
	}
	return resolvedLinkUnknown
}

func resolvedStatusSucceededWithoutStderr(result CommandResult, err error) bool {
	return commandSucceeded(result, err) && result.RawStderr == "" && result.Stderr == ""
}

// parseManagedResolvedLinkStatus accepts the Ubuntu 24.04 single-link status
// shape used by recovery authority. It intentionally rejects unknown fields and
// malformed/partial target sections. A future systemd-resolved format change
// therefore becomes unknown/incomplete inspection until explicitly supported.
func parseManagedResolvedLinkStatus(output string) (netsnapshot.ResolvedLink, bool) {
	var link netsnapshot.ResolvedLink
	seenHeader := false
	seenFields := make(map[string]bool)
	lastField := ""

	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Link ") {
			if seenHeader {
				return netsnapshot.ResolvedLink{}, false
			}
			index, ok := parseManagedResolvedLinkHeader(line)
			if !ok {
				return netsnapshot.ResolvedLink{}, false
			}
			link = netsnapshot.ResolvedLink{Index: index, Name: managedInterface}
			seenHeader = true
			lastField = ""
			continue
		}
		if !seenHeader {
			return netsnapshot.ResolvedLink{}, false
		}

		key, value, hasSeparator := strings.Cut(line, ":")
		if !hasSeparator {
			if !applyResolvedContinuation(&link, lastField, line) {
				return netsnapshot.ResolvedLink{}, false
			}
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || seenFields[key] {
			return netsnapshot.ResolvedLink{}, false
		}

		switch key {
		case "Current Scopes":
			if value == "" {
				return netsnapshot.ResolvedLink{}, false
			}
			link.CurrentScopes = appendResolvedTokens(nil, value)
		case "Protocols":
			if value == "" {
				return netsnapshot.ResolvedLink{}, false
			}
			link.Protocols = appendResolvedTokens(nil, value)
		case "Current DNS Server":
			fields := strings.Fields(value)
			if len(fields) != 1 {
				return netsnapshot.ResolvedLink{}, false
			}
			link.CurrentDNSServer = fields[0]
		case "DNS Servers":
			if value == "" {
				return netsnapshot.ResolvedLink{}, false
			}
			link.DNSServers = appendResolvedTokens(nil, value)
		case "DNS Domain":
			if value == "" {
				return netsnapshot.ResolvedLink{}, false
			}
			link.DNSDomains = appendResolvedTokens(nil, value)
		default:
			return netsnapshot.ResolvedLink{}, false
		}
		seenFields[key] = true
		lastField = key
	}

	if !seenHeader || !seenFields["Current Scopes"] || !seenFields["Protocols"] {
		return netsnapshot.ResolvedLink{}, false
	}
	return link, true
}

func parseManagedResolvedLinkHeader(line string) (string, bool) {
	const prefix = "Link "
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, ")") {
		return "", false
	}
	open := strings.LastIndex(line, " (")
	if open <= len(prefix) {
		return "", false
	}
	index := strings.TrimSpace(line[len(prefix):open])
	name := strings.TrimSpace(line[open+2 : len(line)-1])
	if index == "" || name != managedInterface {
		return "", false
	}
	for _, r := range index {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return index, true
}

func applyResolvedContinuation(link *netsnapshot.ResolvedLink, field, line string) bool {
	if link == nil || strings.TrimSpace(line) == "" {
		return false
	}
	switch field {
	case "DNS Servers":
		link.DNSServers = appendResolvedTokens(link.DNSServers, line)
		return true
	case "DNS Domain":
		link.DNSDomains = appendResolvedTokens(link.DNSDomains, line)
		return true
	default:
		return false
	}
}

func appendResolvedTokens(values []string, text string) []string {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		seen[value] = true
	}
	for _, token := range strings.Fields(text) {
		if token == "" || seen[token] {
			continue
		}
		seen[token] = true
		values = append(values, token)
	}
	return values
}

func resolvedLinkHasPodlazConfiguration(link netsnapshot.ResolvedLink) bool {
	return containsResolvedValue(link.DNSDomains, resolvedRouteOnlyDomain) &&
		resolvedDefaultRoutePolarityOf(link.Protocols) == resolvedDefaultRoutePositive &&
		(strings.TrimSpace(link.CurrentDNSServer) != "" || len(link.DNSServers) > 0)
}

func resolvedLinkHasConcreteDNSConfiguration(link netsnapshot.ResolvedLink) bool {
	polarity := resolvedDefaultRoutePolarityOf(link.Protocols)
	return strings.TrimSpace(link.CurrentDNSServer) != "" ||
		len(link.DNSServers) > 0 ||
		len(link.DNSDomains) > 0 ||
		polarity == resolvedDefaultRoutePositive ||
		polarity == resolvedDefaultRouteConflicting
}

func resolvedLinkIsProvenEmptyTransient(link netsnapshot.ResolvedLink) bool {
	return len(link.CurrentScopes) == 1 && link.CurrentScopes[0] == "none" &&
		strings.TrimSpace(link.CurrentDNSServer) == "" &&
		len(link.DNSServers) == 0 &&
		len(link.DNSDomains) == 0 &&
		resolvedDefaultRoutePolarityOf(link.Protocols) == resolvedDefaultRouteNegative
}

func resolvedDefaultRoutePolarityOf(protocols []string) resolvedDefaultRoutePolarity {
	positive := containsResolvedValue(protocols, resolvedDefaultRouteProtocol)
	negative := containsResolvedValue(protocols, resolvedNoDefaultRouteProtocol)
	switch {
	case positive && negative:
		return resolvedDefaultRouteConflicting
	case positive:
		return resolvedDefaultRoutePositive
	case negative:
		return resolvedDefaultRouteNegative
	default:
		return resolvedDefaultRouteUnknown
	}
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
		return "systemd-resolved link status is malformed, ambiguous, or contains non-podlaz DNS configuration"
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
