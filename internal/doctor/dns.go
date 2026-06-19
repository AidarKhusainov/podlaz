package doctor

import (
	"context"
	"strings"
)

func resolvedDNSDiagnosticLine(ctx context.Context, runner CommandRunner, resolvectlPath string) string {
	result, err := runCommand(ctx, runner, resolvectlPath, "status", managedInterface, "--no-pager")
	if commandSucceeded(result, err) {
		if strings.Contains(result.Stdout, "~.") {
			return "podlaz DNS route-only domain ~. active on " + managedInterface
		}
		return "podlaz DNS link exists without route-only domain ~. on " + managedInterface
	}
	if resourceMissing(result) {
		return "no podlaz-owned DNS state found for " + managedInterface
	}
	return "podlaz DNS state unknown for " + managedInterface + ": " + commandFailureMessage(result, err)
}
