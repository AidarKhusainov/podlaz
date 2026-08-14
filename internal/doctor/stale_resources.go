package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type staleResourceOptions struct {
	ipPath                  string
	ipOK                    bool
	nftPath                 string
	nftOK                   bool
	runtimeDir              string
	runtimeDirOwnedByDaemon bool
	lifecycle               LifecycleDiagnosticContext
}

func staleResources(ctx context.Context, runner CommandRunner, opts staleResourceOptions) Check {
	var stale []string
	warnings := lifecycleContextWarnings(opts.lifecycle)

	inspectManagedInterface(ctx, runner, opts, &stale, &warnings)
	inspectManagedNFTTable(ctx, runner, opts, &stale, &warnings)

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

	appendTransactionState(&stale, &warnings, opts.runtimeDir)

	message := staleResourceMessage(stale, warnings, opts.lifecycle)
	if len(stale) > 0 || len(warnings) > 0 {
		return Check{Name: "stale-resources", Severity: SeverityWarning, Message: message}
	}
	return Check{Name: "stale-resources", Severity: SeverityOK, Message: message}
}

func inspectManagedInterface(ctx context.Context, runner CommandRunner, opts staleResourceOptions, stale, warnings *[]string) {
	if !opts.ipOK {
		*warnings = append(*warnings, fmt.Sprintf("cannot inspect interface %s because ip is unavailable", managedInterface))
		return
	}

	args := []string{"link", "show", "dev", managedInterface}
	if opts.lifecycle.Interface == ManagedResourceExactOwned {
		args = []string{"-details", "-o", "link", "show", "dev", managedInterface}
	}
	result, err := runCommand(ctx, runner, opts.ipPath, args...)
	switch {
	case commandSucceeded(result, err):
		switch opts.lifecycle.Interface {
		case ManagedResourceExactOwned:
			name, index, kind, ok := parseManagedLinkIdentity(result.Stdout)
			if !ok || name != managedInterface || index != opts.lifecycle.InterfaceLinkIndex || kind != opts.lifecycle.InterfaceLinkKind || index <= 0 || kind != "tun" {
				*warnings = append(*warnings, fmt.Sprintf("cannot prove interface %s belongs to the active transaction", managedInterface))
			}
		case ManagedResourceUnproven:
			*warnings = append(*warnings, fmt.Sprintf("cannot prove interface %s belongs to the active transaction", managedInterface))
		default:
			*stale = append(*stale, fmt.Sprintf("interface %s exists", managedInterface))
		}
	case resourceMissing(result):
		switch opts.lifecycle.Interface {
		case ManagedResourceExactOwned:
			*warnings = append(*warnings, fmt.Sprintf("expected interface %s is missing", managedInterface))
		case ManagedResourceUnproven:
			*warnings = append(*warnings, fmt.Sprintf("cannot prove whether interface %s should exist for the active transaction", managedInterface))
		}
	case commandFailedUnexpectedly(result, err):
		*warnings = append(*warnings, fmt.Sprintf("cannot inspect interface %s: %s", managedInterface, commandFailureMessage(result, err)))
	}
}

func inspectManagedNFTTable(ctx context.Context, runner CommandRunner, opts staleResourceOptions, stale, warnings *[]string) {
	if !opts.nftOK {
		*warnings = append(*warnings, fmt.Sprintf("cannot inspect nft table %s because nft is unavailable", managedNFTTable))
		return
	}

	result, err := runCommand(ctx, runner, opts.nftPath, "list", "table", "inet", "podlaz")
	switch {
	case commandSucceeded(result, err):
		switch opts.lifecycle.NFTTable {
		case ManagedResourceExactOwned:
			// nftables table identity is the exact family/name tuple. The daemon
			// only supplies ExactOwned when the active transaction owns this tuple.
		case ManagedResourceUnproven:
			*warnings = append(*warnings, fmt.Sprintf("cannot prove nft table %s belongs to the active transaction", managedNFTTable))
		default:
			*stale = append(*stale, fmt.Sprintf("nft table %s exists", managedNFTTable))
		}
	case resourceMissing(result):
		switch opts.lifecycle.NFTTable {
		case ManagedResourceExactOwned:
			*warnings = append(*warnings, fmt.Sprintf("expected nft table %s is missing", managedNFTTable))
		case ManagedResourceUnproven:
			*warnings = append(*warnings, fmt.Sprintf("cannot prove whether nft table %s should exist for the active transaction", managedNFTTable))
		}
	case commandFailedUnexpectedly(result, err):
		*warnings = append(*warnings, fmt.Sprintf("cannot inspect nft table %s: %s", managedNFTTable, commandFailureMessage(result, err)))
	}
}

func lifecycleContextWarnings(lifecycle LifecycleDiagnosticContext) []string {
	if lifecycle.State != LifecycleActiveTUN {
		return nil
	}
	warnings := make([]string, 0, 2)
	switch {
	case strings.TrimSpace(lifecycle.TransactionID) == "":
		warnings = append(warnings, "active TUN lifecycle has no transaction id")
	case lifecycle.TransactionState == "":
		warnings = append(warnings, fmt.Sprintf("active transaction %s could not be confirmed", lifecycle.TransactionID))
	case lifecycle.TransactionState != txstate.TransactionCommitted:
		warnings = append(warnings, fmt.Sprintf("active transaction %s is %s, expected committed", lifecycle.TransactionID, lifecycle.TransactionState))
	}
	if lifecycle.TransactionRequiresCleanup {
		warnings = append(warnings, fmt.Sprintf("active transaction %s requires cleanup", lifecycle.TransactionID))
	}
	return warnings
}

func parseManagedLinkIdentity(output string) (string, int, string, bool) {
	line := ""
	for _, candidate := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(candidate) != "" {
			line = strings.TrimSpace(candidate)
			break
		}
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", 0, "", false
	}
	index, err := strconv.Atoi(strings.TrimSuffix(fields[0], ":"))
	if err != nil || index <= 0 {
		return "", 0, "", false
	}
	name := strings.Split(strings.TrimSuffix(fields[1], ":"), "@")[0]
	kind := ""
	for i := 0; i+2 < len(fields); i++ {
		if fields[i] == "tun" && fields[i+1] == "type" && fields[i+2] == "tun" {
			kind = "tun"
			break
		}
	}
	return name, index, kind, name != "" && kind != ""
}

func appendTransactionState(stale *[]string, warnings *[]string, runtimeDir string) {
	summaries, scanWarnings := txstate.ScanTransactions(runtimeDir)
	for _, summary := range summaries {
		if !summary.RequiresCleanup {
			continue
		}
		*stale = append(*stale, fmt.Sprintf(
			"transaction %s %s; rollback available: %s; state path: %s",
			summary.ID,
			summary.StatusLine(),
			summary.RollbackLine(),
			summary.Path,
		))
	}
	for _, warning := range scanWarnings {
		*warnings = append(*warnings, "cannot inspect transaction state: "+warning)
	}
}

func staleResourceMessage(stale []string, warnings []string, lifecycle LifecycleDiagnosticContext) string {
	parts := make([]string, 0, 2)
	if len(stale) > 0 {
		parts = append(parts, "found "+strings.Join(stale, "; "))
	}
	if len(warnings) > 0 {
		parts = append(parts, "incomplete checks: "+strings.Join(warnings, "; "))
	}
	if len(parts) == 0 {
		if lifecycle.State == LifecycleActiveTUN || lifecycle.State == LifecycleActiveProxy {
			return "managed resources match active lifecycle"
		}
		return "no podlaz-owned resources found"
	}
	return strings.Join(parts, "; ")
}
