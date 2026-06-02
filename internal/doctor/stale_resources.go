package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

type staleResourceOptions struct {
	ipPath                  string
	ipOK                    bool
	nftPath                 string
	nftOK                   bool
	runtimeDir              string
	runtimeDirOwnedByDaemon bool
}

func staleResources(ctx context.Context, runner CommandRunner, opts staleResourceOptions) Check {
	var stale []string
	var warnings []string

	if opts.ipOK {
		result, err := runCommand(ctx, runner, opts.ipPath, "link", "show", "dev", managedInterface)
		switch {
		case commandSucceeded(result, err):
			stale = append(stale, fmt.Sprintf("interface %s exists", managedInterface))
		case resourceMissing(result):
		case commandFailedUnexpectedly(result, err):
			warnings = append(warnings, fmt.Sprintf("cannot inspect interface %s: %s", managedInterface, commandFailureMessage(result, err)))
		}
	} else {
		warnings = append(warnings, fmt.Sprintf("cannot inspect interface %s because ip is unavailable", managedInterface))
	}

	if opts.nftOK {
		result, err := runCommand(ctx, runner, opts.nftPath, "list", "table", "inet", "tunwarden")
		switch {
		case commandSucceeded(result, err):
			stale = append(stale, fmt.Sprintf("nft table %s exists", managedNFTTable))
		case resourceMissing(result):
		case commandFailedUnexpectedly(result, err):
			warnings = append(warnings, fmt.Sprintf("cannot inspect nft table %s: %s", managedNFTTable, commandFailureMessage(result, err)))
		}
	} else {
		warnings = append(warnings, fmt.Sprintf("cannot inspect nft table %s because nft is unavailable", managedNFTTable))
	}

	if stat, err := os.Stat(opts.runtimeDir); err == nil {
		if stat.IsDir() && opts.runtimeDirOwnedByDaemon {
			// A live daemon owns its runtime directory, so its mere presence is not stale state.
		} else if stat.IsDir() {
			stale = append(stale, fmt.Sprintf("runtime directory %s exists", opts.runtimeDir))
		} else {
			stale = append(stale, fmt.Sprintf("runtime path %s exists", opts.runtimeDir))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		warnings = append(warnings, fmt.Sprintf("cannot inspect runtime directory %s: %v", opts.runtimeDir, err))
	}

	message := staleResourceMessage(stale, warnings)
	if len(stale) > 0 || len(warnings) > 0 {
		return Check{Name: "stale-resources", Severity: SeverityWarning, Message: message}
	}
	return Check{Name: "stale-resources", Severity: SeverityOK, Message: message}
}

func staleResourceMessage(stale []string, warnings []string) string {
	parts := make([]string, 0, 2)
	if len(stale) > 0 {
		parts = append(parts, "found "+strings.Join(stale, "; "))
	}
	if len(warnings) > 0 {
		parts = append(parts, "incomplete checks: "+strings.Join(warnings, "; "))
	}
	if len(parts) == 0 {
		return "no TunWarden-owned resources found"
	}
	return strings.Join(parts, "; ")
}
