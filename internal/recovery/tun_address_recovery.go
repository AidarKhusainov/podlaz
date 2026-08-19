package recovery

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type tunAddressRecoveryRunner struct {
	delegate CommandRunner
}

func (r tunAddressRecoveryRunner) Run(ctx context.Context, name string, args ...string) (netexecutor.CommandResult, error) {
	result, err := r.delegate.Run(ctx, name, args...)
	return netexecutor.CommandResult{
		Stdout:    result.Stdout,
		Stderr:    result.Stderr,
		RawStdout: firstNonEmpty(result.RawStdout, result.Stdout),
		RawStderr: firstNonEmpty(result.RawStderr, result.Stderr),
		ExitCode:  result.ExitCode,
	}, err
}

func firstNonEmpty(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

func (e DaemonCleanupExecutor) rollbackTUNAddressResults(ctx context.Context, entries []txstate.TUNAddressRollback, allowMissingLink bool) []CleanupResult {
	results := make([]CleanupResult, 0, len(entries))
	for _, entry := range entries {
		target := fmt.Sprintf("%s@ifindex=%d:%s", entry.InterfaceName, entry.LinkIndex, entry.CIDR)
		candidate := Candidate{Kind: "tun-address", Description: "daemon-owned TUN IPv4 address", Target: target}
		if reason := validateTunAddressRollback(entry); reason != "" {
			results = append(results, skipped(candidate, reason))
			continue
		}
		plan := planner.TunAddressPlan{
			Family:             entry.Family,
			Interface:          entry.InterfaceName,
			CIDR:               entry.CIDR,
			Scope:              entry.Scope,
			Action:             planner.TunAddressActionAssign,
			Owner:              netexecutor.OwnerTunAddress,
			RollbackKey:        entry.InterfaceName + "/" + entry.CIDR,
			LinkIndex:          entry.LinkIndex,
			LinkKind:           entry.LinkKind,
			AppearedAfterCore:  entry.AppearedAfterCore,
			AllowOwnedExisting: true,
			AllowMissingLink:   allowMissingLink,
		}
		executor := netexecutor.IPTunAddressExecutor{Runner: tunAddressRecoveryRunner{delegate: e.Runner}}
		if err := executor.Rollback(ctx, plan); err != nil {
			results = append(results, failed(candidate, err))
			continue
		}
		results = append(results, recovered(candidate))
	}
	return results
}

func validateTunAddressRollback(entry txstate.TUNAddressRollback) string {
	if !ownedRollbackMetadata(entry.Owner, netexecutor.OwnerTunAddress) {
		return "non-podlaz TUN address metadata"
	}
	if entry.InterfaceName != managedInterface || entry.LinkIndex <= 0 || entry.LinkKind != "tun" || !entry.AppearedAfterCore {
		return "ambiguous or incomplete TUN address link identity"
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(entry.CIDR))
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 32 || !planner.IsAllocatedTunIPv4CIDR(prefix.String()) {
		return "TUN address is outside the bounded Podlaz session allocation namespace"
	}
	if entry.Family != "" && entry.Family != "ipv4" {
		return "unsupported TUN address family"
	}
	if entry.Scope != "" && entry.Scope != "global" {
		return "unsupported TUN address scope"
	}
	return ""
}

func trackedChildAbsenceProven(processes []txstate.ChildProcessRollback, results []CleanupResult) bool {
	if len(processes) == 0 || len(results) != len(processes) {
		return false
	}
	for _, result := range results {
		if result.Candidate.Kind != "child-process" || result.Status != "recovered" || !strings.Contains(result.Message, "already absent") {
			return false
		}
	}
	return true
}
