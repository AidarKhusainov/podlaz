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
