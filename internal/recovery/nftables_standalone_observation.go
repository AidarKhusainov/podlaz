package recovery

import (
	"context"
	"fmt"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
)

// inspectStandaloneNftablesCandidate is deliberately read-only. A standalone
// table identity is never cleanup authority; this check exists only so a stale
// candidate captured before an exact transaction rollback can converge once the
// table is proven absent.
func inspectStandaloneNftablesCandidate(ctx context.Context, runner CommandRunner, candidate Candidate) CleanupResult {
	family, table, ok := parseNFTTarget(candidate.Target)
	if !ok || !isManagedNFTTarget(family, table) {
		return skipped(candidate, "non-podlaz nftables target")
	}
	present, err := (netexecutor.NftablesExecutor{Runner: nftablesExecutorCommandRunner(runner)}).TableExists(ctx, family, table)
	if err != nil {
		return failed(candidate, fmt.Errorf("inspect standalone nftables candidate: %w", err))
	}
	if !present {
		return recoveredWithMessage(candidate, "nftables table is already absent")
	}
	return skipped(candidate, "nftables table identity alone is not Podlaz cleanup authority; exact transaction rollback authority is required")
}
