package daemon

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

// tunAutomaticTerminalHandler runs only after the coordinator has atomically
// claimed the current publication and lifecycleOperationLock has admitted the
// automatic terminal mutation. The supplied admission therefore already owns
// the single lifecycle operation token; this handler must never call a wrapped
// lifecycle method that would register/acquire the mutation a second time.
type tunAutomaticTerminalHandler struct {
	store                networkSessionStateStore
	currentTransactionID func() string
	collect              func(context.Context, planner.TunPlan, error) tunFailureDiagnosticSummary
	teardown             func(context.Context) error
	finalize             func(context.Context, tunFailureDiagnosticSummary, string)
	markCleanupRequired  func(tunAutomaticDisposition)
	cleanupTimeout       time.Duration
}

func (h tunAutomaticTerminalHandler) Handle(
	ctx context.Context,
	admission *lifecycleAutomaticAdmission,
	disposition tunAutomaticDisposition,
) {
	if admission == nil {
		return
	}
	defer admission.Release()
	if disposition.Kind != tunDecisionTerminal || disposition.Cause == nil {
		return
	}

	// The operation token closes lifecycle races while the durable session and
	// active transaction identities are rechecked. Any mismatch makes the old
	// automatic decision stale before diagnostics or network mutation begin.
	state, exists, err := h.store.Load()
	if err != nil || !exists || state.Intent != networkSessionIntentResume || state.SessionID != disposition.NetworkSessionID {
		return
	}
	if expectedTransactionID := strings.TrimSpace(disposition.TransactionID); expectedTransactionID != "" {
		if h.currentTransactionID == nil || strings.TrimSpace(h.currentTransactionID()) != expectedTransactionID {
			return
		}
	}

	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}

	var summary tunFailureDiagnosticSummary
	if h.collect != nil {
		diagnosticCtx, cancel := context.WithTimeout(baseCtx, tunFailureDiagnosticRunTimeout)
		summary = h.collect(diagnosticCtx, disposition.Plan, disposition.Cause)
		cancel()
	}

	cleanupTimeout := h.cleanupTimeout
	if cleanupTimeout <= 0 {
		cleanupTimeout = tunRollbackCleanupTimeout
	}
	cleanupCtx, cancel := context.WithTimeout(withTerminalNetworkSessionTeardown(baseCtx), cleanupTimeout)
	var cleanupErr error
	if h.teardown == nil {
		cleanupErr = errors.New("missing admitted terminal Network Session teardown")
	} else {
		cleanupErr = h.teardown(cleanupCtx)
	}
	cancel()

	status := "completed"
	if cleanupErr != nil {
		status = "failed"
		if h.markCleanupRequired != nil {
			h.markCleanupRequired(disposition)
		}
	}
	if h.finalize != nil {
		h.finalize(baseCtx, summary, status)
	}
}

// tunRevalidationTerminalHandler is the pre-#262 compatibility path retained
// until production wiring is moved to tunAutomaticTerminalHandler. It must not
// be used by the final evidence-driven automatic-disposition flow.
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
	cleanupCtx, cancel := context.WithTimeout(withTerminalNetworkSessionTeardown(baseCtx), cleanupTimeout)
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
