package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type tunTerminalDataPlaneCleaner struct {
	current               func() (xrayState, *tunRuntimeProcessIdentity)
	disconnect            func(context.Context) (api.LifecycleResponse, error)
	disconnectTransaction func(context.Context, string) (api.LifecycleResponse, error)
}

func (c tunTerminalDataPlaneCleaner) Cleanup(ctx context.Context, expectedTransactionID string) (api.LifecycleResponse, error) {
	expectedTransactionID = strings.TrimSpace(expectedTransactionID)
	if expectedTransactionID == "" {
		return api.LifecycleResponse{}, errors.New("terminal TUN cleanup requires exact transaction identity")
	}
	if c.current == nil {
		return api.LifecycleResponse{}, errors.New("terminal TUN cleanup requires current runtime identity")
	}
	state, process := c.current()
	if state.Mode != planner.ModeTun || strings.TrimSpace(state.TransactionID) != expectedTransactionID {
		return api.LifecycleResponse{}, fmt.Errorf("terminal TUN cleanup transaction authority changed: current=%q expected=%q", strings.TrimSpace(state.TransactionID), expectedTransactionID)
	}

	switch state.Connection {
	case "active":
		if c.disconnect == nil {
			return api.LifecycleResponse{}, errors.New("terminal active TUN cleanup requires lifecycle disconnect")
		}
		return c.disconnect(ctx)
	case api.ConnectionCoreExited:
		if process != nil {
			return api.LifecycleResponse{}, errors.New("degraded terminal TUN cleanup unexpectedly has a supervised process identity")
		}
		if c.disconnectTransaction == nil {
			return api.LifecycleResponse{}, errors.New("degraded terminal TUN cleanup requires exact transaction rollback")
		}
		return c.disconnectTransaction(ctx, expectedTransactionID)
	default:
		return api.LifecycleResponse{}, fmt.Errorf("terminal TUN cleanup refuses unsupported connection state %q", state.Connection)
	}
}

type tunAutomaticTerminalHandler struct {
	store                networkSessionStateStore
	currentTransactionID func() string
	collect              func(context.Context, planner.TunPlan, error) tunFailureDiagnosticSummary
	teardown             func(context.Context) error
	teardownDisposition  func(context.Context, tunAutomaticDisposition) error
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
	switch {
	case h.teardownDisposition != nil:
		cleanupErr = h.teardownDisposition(cleanupCtx, disposition)
	case h.teardown != nil:
		cleanupErr = h.teardown(cleanupCtx)
	default:
		cleanupErr = errors.New("missing admitted terminal Network Session teardown")
	}
	cancel()

	status := "completed"
	if cleanupErr != nil {
		status = "failed"
		if h.markCleanupRequired != nil {
			h.markCleanupRequired(disposition)
		}
	} else {
		reasonStore := newProductTerminalReasonStore(h.store.runtimeDir, h.store.readBootID)
		_ = reasonStore.Set(api.TerminalReasonVPNRestoreFailed)
	}
	if h.finalize != nil {
		h.finalize(baseCtx, summary, status)
	}
}

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
