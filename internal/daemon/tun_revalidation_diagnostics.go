package daemon

import (
	"context"
	"errors"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func (m *XrayManager) collectTunRevalidationFailureDiagnostics(ctx context.Context, plan planner.TunPlan, cause error) tunFailureDiagnosticSummary {
	if m == nil {
		return tunFailureDiagnosticSummary{}
	}
	m.mu.Lock()
	state := m.state
	coreRunning := m.cmd != nil
	m.mu.Unlock()

	if plan.ProfileID == "" {
		plan.ProfileID = state.ProfileID
	}
	if plan.ProfileName == "" {
		plan.ProfileName = state.ProfileName
	}
	input := tunDiagnosticInput{
		state:          state,
		coreRunning:    coreRunning,
		plan:           plan,
		failurePhase:   tunRevalidationDiagnosticFailurePhase(cause),
		rollbackStatus: "pending",
	}
	if transaction, _, err := (txstate.TransactionStore{RuntimeDir: m.runtimeDir()}).Load(state.TransactionID); err != nil {
		input.metadataError = "load TUN transaction diagnostic metadata: " + err.Error()
	} else {
		input.serverEndpoint, input.serverName = tunDiagnosticServerMetadata(transaction)
	}

	input.snapshot = m.collectTunSnapshot(ctx, tunSnapshotOptionsForState(state))
	report, persisted := m.runAndPersistTunFailureDiagnostics(ctx, input, cause)
	classification := report.PrimaryClassification
	if classification == "" {
		classification = tundiag.ClassInternalDiagnosticError
	}
	return tunFailureDiagnosticSummary{
		PrimaryClassification: classification,
		ReportPath:            report.ReportPath,
		Persisted:             persisted,
	}
}

func tunRevalidationDiagnosticFailurePhase(cause error) string {
	var verificationErr *TunVerificationError
	if errors.As(cause, &verificationErr) && strings.TrimSpace(verificationErr.Phase) != "" {
		return strings.TrimSpace(verificationErr.Phase)
	}
	return tunLifecycleDiagnosticFailurePhase(cause)
}
