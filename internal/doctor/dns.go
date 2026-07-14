package doctor

import (
	"context"
	"strings"
)

type resolvedDNSDiagnostic struct {
	severity Severity
	message  string
}

func resolvedDNSDiagnosticLine(ctx context.Context, runner CommandRunner, resolvectlPath, ipPath string, ipOK bool) resolvedDNSDiagnostic {
	result, err := runCommand(ctx, runner, resolvectlPath, "status", managedInterface, "--no-pager")
	if resolvedResourceMissing(result) {
		return resolvedDNSDiagnostic{severity: SeverityOK, message: "no podlaz-owned DNS state found for " + managedInterface}
	}
	if !commandSucceeded(result, err) {
		return resolvedDNSDiagnostic{severity: SeverityWarning, message: "podlaz DNS state unknown for " + managedInterface + ": " + commandFailureMessage(result, err)}
	}

	if podlazLinkMissing(ctx, runner, ipPath, ipOK) {
		return resolvedDNSDiagnostic{
			severity: SeverityWarning,
			message:  "stale systemd-resolved link record for " + managedInterface + " exists after the interface is missing; the next TUN connect will refresh this podlaz-owned link state automatically",
		}
	}
	if !ipOK {
		return resolvedDNSDiagnostic{
			severity: SeverityWarning,
			message:  "podlaz DNS link state exists but interface state cannot be verified because ip is unavailable; run plz doctor from the packaged daemon or install iproute2",
		}
	}
	if strings.Contains(result.Stdout, "~.") {
		return resolvedDNSDiagnostic{
			severity: SeverityWarning,
			message:  "podlaz DNS route-only domain ~. is active on " + managedInterface + "; this is expected only during an active TUN session; run plz status and plz recover --execute --yes if no TUN session is active",
		}
	}
	return resolvedDNSDiagnostic{
		severity: SeverityWarning,
		message:  "podlaz DNS link exists without route-only domain ~. on " + managedInterface + "; the next TUN connect will refresh this podlaz-owned link state automatically",
	}
}

func resolvedResourceMissing(result CommandResult) bool {
	if resourceMissing(result) {
		return true
	}
	text := strings.ToLower(result.Stdout + " " + result.Stderr)
	return result.ExitCode != 0 && strings.Contains(text, "no such device")
}

func podlazLinkMissing(ctx context.Context, runner CommandRunner, ipPath string, ipOK bool) bool {
	if !ipOK {
		return false
	}
	result, err := runCommand(ctx, runner, ipPath, "link", "show", "dev", managedInterface)
	return resourceMissing(result) || (!commandSucceeded(result, err) && strings.Contains(strings.ToLower(result.Stderr), "does not exist"))
}
