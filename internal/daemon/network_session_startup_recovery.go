package daemon

import (
	"context"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func recoverExactNetworkSessionTransactions(ctx context.Context, runtimeDir string) api.RecoveryResponse {
	// Privacy recovery is the first startup mutation stage. A protected Network
	// Session must have its exact envelope present and verified before any old
	// data-plane transaction can be rolled back.
	if err := reconcileProductionNetworkSessionProtection(ctx, newNetworkSessionStateStore(runtimeDir, nil)); err != nil {
		return api.RecoveryResponse{
			Mode: "execute",
			Warnings: []api.RecoveryWarning{{
				Target:  "network-session privacy protection",
				Message: "exact privacy protection reconciliation failed; data-plane recovery was not started",
			}},
		}
	}
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
