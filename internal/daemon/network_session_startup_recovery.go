package daemon

import (
	"context"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

// recoverExactNetworkSessionTransactions owns only ordinary Data Plane
// Generation transaction recovery. Privacy Envelope/session intent convergence
// is deliberately orchestrated by resumeNetworkSession so startup and /recover
// share one lifecycle ordering instead of recursively reconciling protection.
func recoverExactNetworkSessionTransactions(ctx context.Context, runtimeDir string) api.RecoveryResponse {
	return recoverExactNetworkSessionTransactionsWithOptions(ctx, runtimeDir, recovery.Options{})
}

func recoverExactNetworkSessionTransactionsWithOptions(ctx context.Context, runtimeDir string, opts recovery.Options) api.RecoveryResponse {
	plan := exactNetworkSessionTransactionRecoveryPlan(runtimeDir)
	opts.RuntimeDir = runtimeDir
	opts.Scanner = fixedDaemonRecoveryScanner{plan: plan}
	if opts.Executor == nil {
		opts.Executor = recovery.NetworkSessionCleanupExecutor{RuntimeDir: runtimeDir, Runner: opts.Runner}
	}
	return recoveryResponseToAPI(recovery.ExecuteWithOptions(ctx, opts))
}

func exactNetworkSessionTransactionRecoveryPlan(runtimeDir string) recovery.PlanResult {
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	plan := recovery.PlanResult{
		Candidates: make([]recovery.Candidate, 0, len(summaries)),
		Warnings:   make([]recovery.Warning, 0, len(warnings)),
	}
	for _, summary := range summaries {
		if !summary.RequiresRecovery {
			continue
		}
		plan.Candidates = append(plan.Candidates, recovery.Candidate{
			Kind:        "transaction-state",
			Description: "transaction rollback state",
			Target:      summary.Path,
			Transaction: &recovery.TransactionCandidate{
				ID:                summary.ID,
				State:             string(summary.State),
				Status:            summary.StatusLine(),
				RollbackAvailable: summary.RollbackAvailable,
				RequiresCleanup:   true,
				Path:              summary.Path,
			},
		})
	}
	for _, warning := range warnings {
		plan.Warnings = append(plan.Warnings, recovery.Warning{Target: "transaction state", Message: warning})
	}
	return plan
}
