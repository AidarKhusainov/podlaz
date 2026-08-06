package daemon

import (
	"context"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

type fixedDaemonRecoveryScanner struct{ plan recovery.PlanResult }

func (s fixedDaemonRecoveryScanner) Scan(context.Context) recovery.ScanResult {
	return recovery.ScanResult{
		Candidates: append([]recovery.Candidate(nil), s.plan.Candidates...),
		Warnings:   append([]recovery.Warning(nil), s.plan.Warnings...),
	}
}

func daemonRecover(ctx context.Context, runtimeDir string, status api.StatusResponse) api.RecoveryResponse {
	return daemonRecoverWithOptions(ctx, runtimeDir, status, recovery.Options{})
}

func daemonRecoverWithOptions(ctx context.Context, runtimeDir string, status api.StatusResponse, opts recovery.Options) api.RecoveryResponse {
	opts.RuntimeDir = runtimeDir
	plan := recovery.PlanWithOptions(ctx, opts)
	if status.Connection == "active" {
		return recoveryResponseToAPI(activeRecoveryMutationFreeResult(plan, runtimeDir))
	}
	plan = filterStartupScanForActiveRuntime(plan, status, runtimeDir)
	opts.Scanner = fixedDaemonRecoveryScanner{plan: plan}
	if opts.Executor == nil {
		opts.Executor = recovery.DaemonCleanupExecutor{RuntimeDir: runtimeDir, Runner: opts.Runner}
	}
	result := recovery.ExecuteWithOptions(ctx, opts)
	return recoveryResponseToAPI(result)
}

func activeRecoveryMutationFreeResult(plan recovery.PlanResult, runtimeDir string) recovery.ExecuteResult {
	results := make([]recovery.CleanupResult, 0, len(plan.Candidates))
	for _, candidate := range plan.Candidates {
		results = append(results, recovery.CleanupResult{
			Candidate: candidate,
			Status:    "skipped",
			Message:   "active lifecycle session is present; recovery execute is mutation-free",
		})
	}
	warnings := append([]recovery.Warning(nil), plan.Warnings...)
	warnings = append(warnings, recovery.Warning{
		Target:  runtimeDir,
		Message: "active lifecycle session is present; recovery execute did not authorize cleanup mutations",
	})
	return recovery.ExecuteResult{Results: results, Warnings: warnings}
}

func recoveryResponseToAPI(result recovery.ExecuteResult) api.RecoveryResponse {
	results := make([]api.RecoveryCleanupResult, 0, len(result.Results))
	for _, item := range result.Results {
		results = append(results, api.RecoveryCleanupResult{
			Candidate: recoveryCandidateToAPI(item.Candidate),
			Status:    item.Status,
			Message:   item.Message,
		})
	}
	warnings := make([]api.RecoveryWarning, 0, len(result.Warnings))
	for _, warning := range result.Warnings {
		warnings = append(warnings, api.RecoveryWarning{Target: warning.Target, Message: warning.Message})
	}
	return api.RecoveryResponse{Mode: "execute", Results: results, Warnings: warnings}
}

func recoveryCandidateToAPI(candidate recovery.Candidate) api.RecoveryCandidate {
	out := api.RecoveryCandidate{Kind: candidate.Kind, Description: candidate.Description, Target: candidate.Target}
	if candidate.Transaction != nil {
		out.Transaction = &api.RecoveryTransactionCandidate{
			ID:                candidate.Transaction.ID,
			State:             candidate.Transaction.State,
			Status:            candidate.Transaction.Status,
			RollbackAvailable: candidate.Transaction.RollbackAvailable,
			RequiresCleanup:   candidate.Transaction.RequiresCleanup,
			Path:              candidate.Transaction.Path,
		}
	}
	return out
}
