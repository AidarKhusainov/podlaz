package daemon

import (
	"context"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func daemonRecover(ctx context.Context, runtimeDir string) api.RecoveryResponse {
	result := recovery.ExecuteWithOptions(ctx, recovery.Options{RuntimeDir: runtimeDir, Executor: recovery.DaemonCleanupExecutor{RuntimeDir: runtimeDir}})
	return recoveryResponseToAPI(result)
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
