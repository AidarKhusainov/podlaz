package daemon

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

const tunRollbackCleanupTimeout = 20 * time.Second

var (
	errFullTunnelConnectionBecameActive = errors.New("connection became active while TUN transaction was applying")
	errFullTunnelCoreExitedBeforeCommit = errors.New("Xray exited before TUN transaction commit")
)

type fullTunnelSemanticError struct {
	msg string
	err error
}

func (e fullTunnelSemanticError) Error() string { return e.msg }
func (e fullTunnelSemanticError) Unwrap() error { return e.err }

type fullTunnelCoreHandle struct {
	cmd  *exec.Cmd
	done <-chan struct{}
	pid  int
}

type fullTunnelTransactionRunner struct {
	runtimeDir string
	profile    profile.Profile
	plan       planner.TunPlan
	corePlan   tunCoreRuntimePlan
	executor   tunPlanExecutor
	now        func() time.Time

	beginNetworkTransaction     func(context.Context, string, profile.Profile, planner.TunPlan, func() time.Time) (tunTransactionResult, error)
	applyNetworkTransaction     func(context.Context, tunTransactionResult, tunPlanExecutor) error
	preflightCore               func(context.Context) error
	saveGeneratedConfigMetadata func(txstate.TransactionStore, string, string, time.Time) error
	startCore                   func(context.Context) (fullTunnelCoreHandle, error)
	stopCore                    func(fullTunnelCoreHandle) error
	verifyCoreStarted           func(<-chan struct{}) error
	saveCoreMetadata            func(txstate.TransactionStore, string, string, int, time.Time) error
	verifyConnectivity          func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error
	collectFailureDiagnostics   func(context.Context, string, planner.TunPlan, error) tunFailureDiagnosticSummary
	finalizeFailureDiagnostics  func(context.Context, tunFailureDiagnosticSummary, string)
	commitActiveState           func(txstate.TransactionStore, string, fullTunnelCoreHandle, xrayState) error
	rollbackTransaction         func(context.Context, string, planner.TunPlan, tunPlanExecutor, tunRollbackChildStopper) error
}

func (r *fullTunnelTransactionRunner) run(ctx context.Context) (xrayState, error) {
	r.setDefaults()

	if err := r.preflightCore(ctx); err != nil {
		return xrayState{}, withTunFailurePhase("core-preflight", "", "not-started", err)
	}

	result, err := r.beginNetworkTransaction(ctx, r.runtimeDir, r.profile, r.plan, r.now)
	if err != nil {
		return xrayState{}, withTunFailurePhase("transaction-begin", "", "not-started", err)
	}
	transactionID := result.TransactionID

	if err := maybePauseForE2ETunHook(ctx, transactionID); err != nil {
		status, convergedErr := r.rollbackFailure(ctx, transactionID, emptyTunRollbackPlan(r.plan), err, "E2E hook failure", nil)
		return xrayState{}, withTunFailurePhase("preflight", transactionID, status, convergedErr)
	}
	if err := r.saveGeneratedConfigMetadata(result.Store, transactionID, r.corePlan.RuntimeConfigPath, transactionNow(result.Store)); err != nil {
		status, convergedErr := r.rollbackFailure(ctx, transactionID, emptyTunRollbackPlan(r.plan), err, "generated config metadata failure", nil)
		return xrayState{}, withTunFailurePhase("core-preflight", transactionID, status, convergedErr)
	}

	core, err := r.startCore(ctx)
	if err != nil {
		if rollbackErr := r.rollback(ctx, transactionID, emptyTunRollbackPlan(r.plan), r.executor, nil); rollbackErr != nil {
			if errors.Is(err, errFullTunnelConnectionBecameActive) {
				return xrayState{}, withTunFailurePhase("core-start", transactionID, "failed", errors.Join(err, rollbackErr))
			}
			return xrayState{}, withTunFailurePhase("core-start", transactionID, "failed", errors.Join(err, fmt.Errorf("rollback TUN transaction after Xray start failure: %w", rollbackErr)))
		}
		if errors.Is(err, errFullTunnelConnectionBecameActive) {
			return xrayState{}, withTunFailurePhase("core-start", transactionID, "completed", fullTunnelSemanticError{msg: "connection already active; rolled back newly opened TUN transaction", err: errFullTunnelConnectionBecameActive})
		}
		return xrayState{}, withTunFailurePhase("core-start", transactionID, "completed", err)
	}
	stopCore := r.supervisedCoreStopper(core)

	if err := r.saveCoreMetadata(result.Store, transactionID, r.corePlan.RuntimeConfigPath, core.pid, transactionNow(result.Store)); err != nil {
		status, convergedErr := r.rollbackFailure(ctx, transactionID, emptyTunRollbackPlan(r.plan), err, "core metadata failure", stopCore)
		return xrayState{}, withTunFailurePhase("core-start", transactionID, status, convergedErr)
	}
	if err := r.verifyCoreStarted(core.done); err != nil {
		if rollbackErr := r.rollback(ctx, transactionID, emptyTunRollbackPlan(r.plan), r.executor, stopCore); rollbackErr != nil {
			return xrayState{}, withTunFailurePhase("core-start", transactionID, "failed", errors.Join(err, fmt.Errorf("rollback TUN transaction after Xray startup verification failure: %w", rollbackErr)))
		}
		return xrayState{}, withTunFailurePhase("core-start", transactionID, "completed", fmt.Errorf("%w; rollback completed", err))
	}

	if err := r.applyNetworkTransaction(ctx, result, r.executor); err != nil {
		phase := networkApplyVerifyPhase(err)
		summary := r.collectFailureDiagnostics(ctx, transactionID, r.plan, err)
		recordFailureDiagnosticPersistenceEvent(summary)
		err = withTunFailureDiagnosticSummary(err, summary)
		recordE2ETunHookEvent("rollback-started")
		rollbackErr := r.rollbackNetworkMutation(ctx, transactionID, err, stopCore)
		if rollbackErr != nil {
			r.finalizeFailureDiagnostics(ctx, summary, "failed")
			recordE2ETunHookEvent("diagnostics-finalized-failed")
			recordE2ETunHookEvent("rollback-failed")
			return xrayState{}, withTunFailurePhase(phase, transactionID, "failed", errors.Join(err, fmt.Errorf("rollback TUN transaction after %s failure: %w", phase, rollbackErr)))
		}
		r.finalizeFailureDiagnostics(ctx, summary, "completed")
		recordE2ETunHookEvent("diagnostics-finalized-completed")
		recordE2ETunHookEvent("rollback-completed")
		return xrayState{}, withTunFailurePhase(phase, transactionID, "completed", withTunRollbackCompleted(err))
	}
	if err := r.verifyConnectivity(ctx, r.plan, r.corePlan); err != nil {
		summary := r.collectFailureDiagnostics(ctx, transactionID, r.plan, err)
		recordFailureDiagnosticPersistenceEvent(summary)
		err = withTunFailureDiagnosticSummary(err, summary)
		recordE2ETunHookEvent("rollback-started")
		if rollbackErr := r.rollback(ctx, transactionID, r.plan, r.executor, stopCore); rollbackErr != nil {
			r.finalizeFailureDiagnostics(ctx, summary, "failed")
			recordE2ETunHookEvent("diagnostics-finalized-failed")
			recordE2ETunHookEvent("rollback-failed")
			return xrayState{}, withTunFailurePhase("connectivity-verify", transactionID, "failed", errors.Join(err, fmt.Errorf("rollback TUN transaction after connectivity verification failure: %w", rollbackErr)))
		}
		r.finalizeFailureDiagnostics(ctx, summary, "completed")
		recordE2ETunHookEvent("diagnostics-finalized-completed")
		recordE2ETunHookEvent("rollback-completed")
		return xrayState{}, withTunFailurePhase("connectivity-verify", transactionID, "completed", withTunRollbackCompleted(err))
	}

	active := fullTunnelActiveState(r.profile, r.plan, r.corePlan, transactionID)
	if err := r.commitActiveState(result.Store, transactionID, core, active); err != nil {
		if errors.Is(err, errFullTunnelCoreExitedBeforeCommit) {
			if rollbackErr := r.rollback(ctx, transactionID, r.plan, r.executor, nil); rollbackErr != nil {
				return xrayState{}, withTunFailurePhase("commit", transactionID, "failed", errors.Join(err, rollbackErr))
			}
			return xrayState{}, withTunFailurePhase("commit", transactionID, "completed", fullTunnelSemanticError{msg: "Xray exited before TUN transaction commit; rollback completed", err: errFullTunnelCoreExitedBeforeCommit})
		}
		if rollbackErr := r.rollback(ctx, transactionID, r.plan, r.executor, stopCore); rollbackErr != nil {
			return xrayState{}, withTunFailurePhase("commit", transactionID, "failed", errors.Join(err, fmt.Errorf("rollback TUN transaction after commit failure: %w", rollbackErr)))
		}
		return xrayState{}, withTunFailurePhase("commit", transactionID, "completed", err)
	}

	return active, nil
}

func recordFailureDiagnosticPersistenceEvent(summary tunFailureDiagnosticSummary) {
	if summary.Persisted && strings.TrimSpace(summary.ReportPath) != "" {
		recordE2ETunHookEvent("diagnostics-persisted")
		return
	}
	recordE2ETunHookEvent("diagnostics-not-persisted")
}

func networkApplyVerifyPhase(err error) string {
	var mutationErr *tunNetworkMutationError
	if errors.As(err, &mutationErr) && strings.TrimSpace(mutationErr.Phase()) != "" {
		return mutationErr.Phase()
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "verify tun plan") {
		return "network-verify"
	}
	return "network-apply"
}

func (r *fullTunnelTransactionRunner) supervisedCoreStopper(core fullTunnelCoreHandle) tunRollbackChildStopper {
	return func(txstate.Transaction) error {
		return r.stopCore(core)
	}
}

func (r *fullTunnelTransactionRunner) rollbackNetworkMutation(ctx context.Context, transactionID string, err error, stopChildren tunRollbackChildStopper) error {
	var mutationErr *tunNetworkMutationError
	if !errors.As(err, &mutationErr) {
		return r.rollback(ctx, transactionID, r.plan, r.executor, stopChildren)
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tunRollbackCleanupTimeout)
	defer cancel()
	return mutationErr.RollbackWithChildStopper(cleanupCtx, r.executor, stopChildren)
}

func (r *fullTunnelTransactionRunner) rollbackFailure(ctx context.Context, transactionID string, plan planner.TunPlan, cause error, reason string, stopChildren tunRollbackChildStopper) (string, error) {
	if rollbackErr := r.rollback(ctx, transactionID, plan, r.executor, stopChildren); rollbackErr != nil {
		return "failed", errors.Join(cause, fmt.Errorf("rollback TUN transaction after %s: %w", reason, rollbackErr))
	}
	return "completed", cause
}

func (r *fullTunnelTransactionRunner) rollback(ctx context.Context, transactionID string, plan planner.TunPlan, executor tunPlanExecutor, stopChildren tunRollbackChildStopper) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tunRollbackCleanupTimeout)
	defer cancel()
	return r.rollbackTransaction(cleanupCtx, transactionID, plan, executor, stopChildren)
}

func (r *fullTunnelTransactionRunner) setDefaults() {
	if r.now == nil {
		r.now = time.Now
	}
	if r.beginNetworkTransaction == nil {
		r.beginNetworkTransaction = beginTunTransaction
	}
	if r.applyNetworkTransaction == nil {
		r.applyNetworkTransaction = applyVerifyTunTransactionDeferredRollback
	}
	if r.preflightCore == nil {
		r.preflightCore = func(context.Context) error { return nil }
	}
	if r.saveGeneratedConfigMetadata == nil {
		r.saveGeneratedConfigMetadata = saveGeneratedConfigRollbackMetadata
	}
	if r.startCore == nil {
		r.startCore = func(context.Context) (fullTunnelCoreHandle, error) {
			return fullTunnelCoreHandle{}, errors.New("missing full-tunnel core starter")
		}
	}
	if r.stopCore == nil {
		r.stopCore = func(fullTunnelCoreHandle) error { return nil }
	}
	if r.verifyCoreStarted == nil {
		r.verifyCoreStarted = verifyCoreStarted
	}
	if r.saveCoreMetadata == nil {
		r.saveCoreMetadata = saveCoreRollbackMetadata
	}
	if r.verifyConnectivity == nil {
		r.verifyConnectivity = verifyTunConnectivity
	}
	if r.collectFailureDiagnostics == nil {
		r.collectFailureDiagnostics = func(context.Context, string, planner.TunPlan, error) tunFailureDiagnosticSummary {
			return tunFailureDiagnosticSummary{}
		}
	}
	if r.finalizeFailureDiagnostics == nil {
		r.finalizeFailureDiagnostics = func(context.Context, tunFailureDiagnosticSummary, string) {}
	}
	if r.commitActiveState == nil {
		r.commitActiveState = func(store txstate.TransactionStore, transactionID string, _ fullTunnelCoreHandle, _ xrayState) error {
			return commitTunTransaction(store, transactionID)
		}
	}
	if r.rollbackTransaction == nil {
		r.rollbackTransaction = func(ctx context.Context, transactionID string, plan planner.TunPlan, executor tunPlanExecutor, stopChildren tunRollbackChildStopper) error {
			return rollbackVerifiedTunTransactionWithChildStopper(ctx, r.runtimeDir, transactionID, plan, executor, stopChildren)
		}
	}
}

func fullTunnelActiveState(p profile.Profile, plan planner.TunPlan, corePlan tunCoreRuntimePlan, transactionID string) xrayState {
	return xrayState{
		Connection:        "active",
		Mode:              planner.ModeTun,
		ProfileID:         p.ID,
		ProfileName:       p.Name,
		Proxy:             corePlan.Status,
		TUN:               fmt.Sprintf("enabled (%s)", plan.TunDevice.Name),
		Routes:            fmt.Sprintf("applied %d route(s) and %d policy rule(s)", len(appliedRoutes(plan)), len(appliedPolicyRules(plan))),
		DNS:               dnsStatusLine(plan.DNS),
		Firewall:          firewallStatusLine(plan.Firewall),
		RuntimeConfigPath: corePlan.RuntimeConfigPath,
		TransactionID:     transactionID,
		Warnings:          append(append([]string{}, corePlan.Warnings...), plan.Warnings...),
	}
}

func rollbackVerifiedTunTransaction(ctx context.Context, runtimeDir, transactionID string, plan planner.TunPlan, executor tunPlanExecutor) error {
	return rollbackVerifiedTunTransactionWithChildStopper(ctx, runtimeDir, transactionID, plan, executor, stopRollbackChildProcesses)
}

func emptyTunRollbackPlan(plan planner.TunPlan) planner.TunPlan {
	return planner.TunPlan{
		Mode:        plan.Mode,
		TunnelMode:  plan.TunnelMode,
		ProfileID:   plan.ProfileID,
		ProfileName: plan.ProfileName,
	}
}
