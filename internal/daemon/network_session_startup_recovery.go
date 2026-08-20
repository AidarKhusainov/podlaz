package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func recoverExactNetworkSessionTransactions(ctx context.Context, runtimeDir string) api.RecoveryResponse {
	// Privacy recovery is the first startup mutation stage for a protected
	// Network Session. Unprotected continuation state is intentionally ignored
	// here so legacy/current-boot reconnect semantics remain unchanged.
	if persistedProtection, err := hasPersistedNetworkSessionProtection(runtimeDir); err != nil || persistedProtection {
		if reconcileErr := reconcileProductionNetworkSessionProtection(ctx, newNetworkSessionStateStore(runtimeDir, nil)); reconcileErr != nil {
			return api.RecoveryResponse{
				Mode: "execute",
				Warnings: []api.RecoveryWarning{{
					Target:  "network-session privacy protection",
					Message: "exact privacy protection reconciliation failed; data-plane recovery was not started",
				}},
			}
		}
	}
	return recoverExactNetworkSessionTransactionsWithOptions(ctx, runtimeDir, recovery.Options{})
}

// hasPersistedNetworkSessionProtection is recognition only, never mutation
// authority. It lets startup decide whether the strict session-state loader must
// reconcile a Privacy Envelope without forcing unprotected legacy continuation
// records through a different boot-id reader. Malformed/unsupported records are
// treated as possibly protected so the strict loader fails closed.
func hasPersistedNetworkSessionProtection(runtimeDir string) (bool, error) {
	data, err := os.ReadFile(newNetworkSessionContinuationStore(runtimeDir, nil).path())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if len(data) > maxNetworkSessionStateBytes {
		return true, errors.New("network session state exceeds maximum size")
	}
	var envelope struct {
		SchemaVersion string          `json:"schema_version"`
		Protection    json.RawMessage `json:"protection"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return true, err
	}
	if envelope.SchemaVersion == networkSessionContinuationSchemaVersion {
		return false, nil
	}
	if envelope.SchemaVersion != networkSessionStateSchemaVersion {
		return true, errors.New("unsupported network session state schema")
	}
	protection := strings.TrimSpace(string(envelope.Protection))
	return protection != "" && protection != "null", nil
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
