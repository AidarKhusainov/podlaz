package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type tunRevalidationTerminalHandler struct {
	collect             func(context.Context, planner.TunPlan, error) tunFailureDiagnosticSummary
	disconnect          func(context.Context) error
	finalize            func(context.Context, tunFailureDiagnosticSummary, string)
	markCleanupRequired func(tunRevalidationOutcome)
	cleanupTimeout      time.Duration
}

func (h tunRevalidationTerminalHandler) Handle(ctx context.Context, outcome tunRevalidationOutcome) {
	if !outcome.needsLifecycleCleanup() {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		return
	}

	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}

	var summary tunFailureDiagnosticSummary
	if h.collect != nil {
		diagnosticCtx, cancel := context.WithTimeout(baseCtx, tunFailureDiagnosticRunTimeout)
		summary = h.collect(diagnosticCtx, outcome.plan, outcome.cause)
		cancel()
	}

	cleanupTimeout := h.cleanupTimeout
	if cleanupTimeout <= 0 {
		cleanupTimeout = tunRollbackCleanupTimeout
	}
	cleanupCtx, cancel := context.WithTimeout(baseCtx, cleanupTimeout)
	var cleanupErr error
	if h.disconnect == nil {
		cleanupErr = errors.New("missing terminal revalidation disconnect")
	} else {
		cleanupErr = h.disconnect(cleanupCtx)
	}
	cancel()

	status := "completed"
	if cleanupErr != nil {
		status = "failed"
		if h.markCleanupRequired != nil {
			h.markCleanupRequired(outcome)
		}
	}
	if h.finalize != nil {
		h.finalize(baseCtx, summary, status)
	}
}
