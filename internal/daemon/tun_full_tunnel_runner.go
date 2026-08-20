package daemon

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
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
	bindTunAddress              func(context.Context, planner.TunPlan, tunPlanExecutor, fullTunnelCoreHandle) (planner.TunPlan, error)
	saveTunAddressMetadata      func(txstate.TransactionStore, string, planner.TunAddressPlan, time.Time) error
	verifyConnectivity          func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error
	armPrivacyEnvelope          func(context.Context, planner.TunPlan) error
	verifyPrivacyEnvelope       func(context.Context) error
	cleanupPrivacyEnvelope      func(context.Context) error
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

	boundPlan, err := r.bindTunAddress(ctx, r.plan, r.executor, core)
	if err != nil {
		status, convergedErr := r.rollbackFailure(ctx, transactionID, emptyTunRollbackPlan(r.plan), err, "TUN address identity binding failure", stopCore)
		return xrayState{}, withTunFailurePhase("tun-address-verify", transactionID, status, convergedErr)
	}
	if strings.TrimSpace(boundPlan.TunAddress.CIDR) != "" {
		if err := r.saveTunAddressMetadata(result.Store, transactionID, boundPlan.TunAddress, transactionNow(result.Store)); err != nil {
			status, convergedErr := r.rollbackFailure(ctx, transactionID, emptyTunRollbackPlan(r.plan), err, "TUN address identity metadata failure", stopCore)
			return xrayState{}, withTunFailurePhase("tun-address-verify", transactionID, status, convergedErr)
		}
	}
	r.plan = boundPlan
	result.Plan = boundPlan

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

	// Initial connectivity verification proves that the data plane works before
	// the persistent privacy boundary is introduced. CONNECTED is still not
	// publishable at this point: the same critical path is verified again after
	// the Privacy Envelope is active.
	if err := r.verifyConnectivity(ctx, r.plan, r.corePlan); err != nil {
		return r.failBeforePrivacyEnvelope(ctx, transactionID, core, stopCore, err)
	}
	if err := r.armPrivacyEnvelope(ctx, r.plan); err != nil {
		return r.failAfterPrivacyEnvelope(ctx, transactionID, stopCore, "privacy-envelope-arm", err)
	}
	if err := r.verifyPrivacyEnvelope(ctx); err != nil {
		return r.failAfterPrivacyEnvelope(ctx, transactionID, stopCore, "privacy-envelope-verify", err)
	}
	if err := r.verifyConnectivity(ctx, r.plan, r.corePlan); err != nil {
		return r.failAfterPrivacyEnvelope(ctx, transactionID, stopCore, "privacy-connectivity-verify", err)
	}

	active := fullTunnelActiveState(r.profile, r.plan, r.corePlan, transactionID)
	if err := r.commitActiveState(result.Store, transactionID, core, active); err != nil {
		stopChildren := stopCore
		if errors.Is(err, errFullTunnelCoreExitedBeforeCommit) {
			stopChildren = nil
		}
		if cleanupErr := r.rollbackDataPlaneThenPrivacy(ctx, transactionID, r.plan, stopChildren); cleanupErr != nil {
			return xrayState{}, withTunFailurePhase("commit", transactionID, "failed", errors.Join(err, cleanupErr))
		}
		if errors.Is(err, errFullTunnelCoreExitedBeforeCommit) {
			return xrayState{}, withTunFailurePhase("commit", transactionID, "completed", fullTunnelSemanticError{msg: "Xray exited before TUN transaction commit; rollback completed", err: errFullTunnelCoreExitedBeforeCommit})
		}
		return xrayState{}, withTunFailurePhase("commit", transactionID, "completed", err)
	}

	return active, nil
}

func (r *fullTunnelTransactionRunner) failBeforePrivacyEnvelope(
	ctx context.Context,
	transactionID string,
	_ fullTunnelCoreHandle,
	stopCore tunRollbackChildStopper,
	cause error,
) (xrayState, error) {
	summary := r.collectFailureDiagnostics(ctx, transactionID, r.plan, cause)
	recordFailureDiagnosticPersistenceEvent(summary)
	cause = withTunFailureDiagnosticSummary(cause, summary)
	recordE2ETunHookEvent("rollback-started")
	if rollbackErr := r.rollback(ctx, transactionID, r.plan, r.executor, stopCore); rollbackErr != nil {
		r.finalizeFailureDiagnostics(ctx, summary, "failed")
		recordE2ETunHookEvent("diagnostics-finalized-failed")
		recordE2ETunHookEvent("rollback-failed")
		return xrayState{}, withTunFailurePhase("connectivity-verify", transactionID, "failed", errors.Join(cause, fmt.Errorf("rollback TUN transaction after connectivity verification failure: %w", rollbackErr)))
	}
	r.finalizeFailureDiagnostics(ctx, summary, "completed")
	recordE2ETunHookEvent("diagnostics-finalized-completed")
	recordE2ETunHookEvent("rollback-completed")
	return xrayState{}, withTunFailurePhase("connectivity-verify", transactionID, "completed", withTunRollbackCompleted(cause))
}

func (r *fullTunnelTransactionRunner) failAfterPrivacyEnvelope(
	ctx context.Context,
	transactionID string,
	stopCore tunRollbackChildStopper,
	phase string,
	cause error,
) (xrayState, error) {
	summary := r.collectFailureDiagnostics(ctx, transactionID, r.plan, cause)
	recordFailureDiagnosticPersistenceEvent(summary)
	cause = withTunFailureDiagnosticSummary(cause, summary)
	recordE2ETunHookEvent("rollback-started")
	if cleanupErr := r.rollbackDataPlaneThenPrivacy(ctx, transactionID, r.plan, stopCore); cleanupErr != nil {
		r.finalizeFailureDiagnostics(ctx, summary, "failed")
		recordE2ETunHookEvent("diagnostics-finalized-failed")
		recordE2ETunHookEvent("rollback-failed")
		return xrayState{}, withTunFailurePhase(phase, transactionID, "failed", errors.Join(cause, cleanupErr))
	}
	r.finalizeFailureDiagnostics(ctx, summary, "completed")
	recordE2ETunHookEvent("diagnostics-finalized-completed")
	recordE2ETunHookEvent("rollback-completed")
	return xrayState{}, withTunFailurePhase(phase, transactionID, "completed", withTunRollbackCompleted(cause))
}

// rollbackDataPlaneThenPrivacy is deliberately asymmetric: an incomplete
// exact data-plane rollback returns immediately and leaves the Privacy Envelope
// armed. Only a fully converged data-plane teardown is allowed to remove the
// session-wide direct-egress barrier.
func (r *fullTunnelTransactionRunner) rollbackDataPlaneThenPrivacy(
	ctx context.Context,
	transactionID string,
	plan planner.TunPlan,
	stopChildren tunRollbackChildStopper,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tunRollbackCleanupTimeout)
	defer cancel()
	if err := r.rollbackTransaction(cleanupCtx, transactionID, plan, r.executor, stopChildren); err != nil {
		return fmt.Errorf("rollback TUN data plane while privacy envelope remains armed: %w", err)
	}
	if err := r.cleanupPrivacyEnvelope(cleanupCtx); err != nil {
		return fmt.Errorf("remove privacy envelope after exact data-plane rollback: %w", err)
	}
	return nil
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
	if r.bindTunAddress == nil {
		r.bindTunAddress = bindTunAddressWithExecutor
	}
	if r.saveTunAddressMetadata == nil {
		r.saveTunAddressMetadata = saveTunAddressIdentityMetadata
	}
	if r.verifyConnectivity == nil {
		r.verifyConnectivity = verifyTunConnectivity
	}
	if r.armPrivacyEnvelope == nil {
		r.armPrivacyEnvelope = func(context.Context, planner.TunPlan) error { return nil }
	}
	if r.verifyPrivacyEnvelope == nil {
		r.verifyPrivacyEnvelope = func(context.Context) error { return nil }
	}
	if r.cleanupPrivacyEnvelope == nil {
		r.cleanupPrivacyEnvelope = func(context.Context) error { return nil }
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

type tunAddressIdentityBinder interface {
	BindTunAddress(context.Context, planner.TunPlan, netexecutor.TunLinkCreationProof) (planner.TunPlan, error)
}

func bindTunAddressWithExecutor(ctx context.Context, plan planner.TunPlan, executor tunPlanExecutor, core fullTunnelCoreHandle) (planner.TunPlan, error) {
	if strings.TrimSpace(plan.TunAddress.CIDR) == "" {
		return plan, nil
	}
	binder, ok := executor.(tunAddressIdentityBinder)
	if !ok {
		return plan, errors.New("TUN executor cannot bind daemon-owned address to the Xray-created link identity")
	}
	proof := netexecutor.TunLinkCreationProof{
		PreStartAbsent: tunLinkAuthoritativelyAbsentBeforeCore(plan.Snapshot, plan.TunAddress.Interface),
		TrackedCorePID: core.pid,
		CoreDone:       core.done,
	}
	return binder.BindTunAddress(ctx, plan, proof)
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

func tunLinkAuthoritativelyAbsentBeforeCore(snapshot netsnapshot.Snapshot, name string) bool {
	matched := false
	for _, device := range snapshot.TunDevices {
		if strings.TrimSpace(device.Name) != strings.TrimSpace(name) {
			continue
		}
		matched = true
		if device.Status != netsnapshot.StatusMissing {
			return false
		}
	}
	return matched
}
