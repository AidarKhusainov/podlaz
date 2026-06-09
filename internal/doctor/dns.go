package doctor

import (
	"context"
	"strings"
)

func resolvedDNSDiagnosticLine(ctx context.Context, runner CommandRunner, resolvectlPath string) string {
	result, err := runCommand(ctx, runner, resolvectlPath, "status", managedInterface, "--no-pager")
	if commandSucceeded(result, err) {
		if strings.Contains(result.Stdout, "~.") {
			return "TunWarden DNS route-only domain ~. active on " + managedInterface
		}
		return "TunWarden DNS link exists without route-only domain ~. on " + managedInterface
	}
	if resourceMissing(result) {
		return "no TunWarden-owned DNS state found for " + managedInterface
	}
	return "TunWarden DNS state unknown for " + managedInterface + ": " + commandFailureMessage(result, err)
}

func dnsState(ctx context.Context, runner CommandRunner, resolvectlPath string, resolvectlOK bool) []Check {
	if !resolvectlOK {
		return []Check{
			{Name: "dns-backend", Severity: SeverityWarning, Message: "systemd-resolved unavailable: resolvectl not found"},
			{Name: "dns-tunwarden-link", Severity: SeverityWarning, Message: "cannot inspect TunWarden DNS state because resolvectl is unavailable"},
		}
	}

	result, err := runCommand(ctx, runner, resolvectlPath, "status", managedInterface, "--no-pager")
	if commandSucceeded(result, err) {
		message := dnsLinkMessage(result.Stdout)
		severity := SeverityOK
		if !strings.Contains(result.Stdout, "~.") {
			severity = SeverityWarning
		}
		return []Check{
			{Name: "dns-backend", Severity: SeverityOK, Message: "systemd-resolved per-link DNS inspectable"},
			{Name: "dns-tunwarden-link", Severity: severity, Message: message},
		}
	}
	if resourceMissing(result) {
		return []Check{
			{Name: "dns-backend", Severity: SeverityOK, Message: "systemd-resolved per-link DNS inspectable"},
			{Name: "dns-tunwarden-link", Severity: SeverityOK, Message: "no TunWarden-owned DNS state found for " + managedInterface},
		}
	}
	return []Check{
		{Name: "dns-backend", Severity: SeverityFail, Message: "systemd-resolved status unavailable: " + commandFailureMessage(result, err)},
		{Name: "dns-tunwarden-link", Severity: SeverityWarning, Message: "TunWarden DNS state unknown for " + managedInterface},
	}
}

func dnsLinkMessage(output string) string {
	line := singleLine(output)
	switch {
	case strings.Contains(output, "~."):
		return "TunWarden DNS state detected on " + managedInterface + ": " + line
	case line != "":
		return "DNS state exists on " + managedInterface + " but route-only domain ~. is not active: " + line
	default:
		return "DNS state exists on " + managedInterface + " but output was empty"
	}
}
