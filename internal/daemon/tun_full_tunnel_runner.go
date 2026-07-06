package daemon

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

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
	commitActiveState           func(txstate.TransactionStore, string, fullTunnelCoreHandle, xrayState) error
	rollbackTransaction         func(context.Context, string, planner.TunPlan, tunPlanExecutor) error
}

func (r *fullTunnelTransactionRunner) run(ctx context.Context) (xrayState, error) {
	r.setDefaults()

	if err := r.preflightCore(ctx); err != nil {
		return xrayState{}, err
	}

	result, err := r.beginNetworkTransaction(ctx, r.runtimeDir, r.profile, r.plan, r.now)
	if err != nil {
		return xrayState{}, err
	}
	transactionID := result.TransactionID

	if err := maybePauseForE2ETunHook(ctx, transactionID); err != nil {
		return xrayState{}, r.rollbackStarted(ctx, transactionID, "E2E hook failure", emptyTunRollbackPlan(r.plan), err)
	}
	if err := r.saveGeneratedConfigMetadata(result.Store, transactionID, r.corePlan.RuntimeConfigPath, transactionNow(result.Store)); err != nil {
		return xrayState{}, r.rollbackStarted(ctx, transactionID, "generated config metadata failure", emptyTunRollbackPlan(r.plan), err)
	}

	core, err := r.startCore(ctx)
	if err != nil {
		if rollbackErr := r.rollbackTransaction(ctx, transactionID, emptyTunRollbackPlan(r.plan), r.executor); rollbackErr != nil {
			if errors.Is(err, errFullTunnelConnectionBecameActive) {
				return xrayState{}, errors.Join(err, rollbackErr)
			}
			return xrayState{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after Xray start failure: %w", rollbackErr))
		}
		if errors.Is(err, errFullTunnelConnectionBecameActive) {
			return xrayState{}, fullTunnelSemanticError{msg: "connection already active; rolled back newly opened TUN transaction", err: errFullTunnelConnectionBecameActive}
		}
		return xrayState{}, err
	}

	if err := r.saveCoreMetadata(result.Store, transactionID, r.corePlan.RuntimeConfigPath, core.pid, transactionNow(result.Store)); err != nil {
		_ = r.stopCore(core)
		return xrayState{}, r.rollbackStarted(ctx, transactionID, "core metadata failure", emptyTunRollbackPlan(r.plan), err)
	}
	if err := r.verifyCoreStarted(core.done); err != nil {
		_ = r.stopCore(core)
		if rollbackErr := r.rollbackTransaction(ctx, transactionID, emptyTunRollbackPlan(r.plan), r.executor); rollbackErr != nil {
			return xrayState{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after Xray startup verification failure: %w", rollbackErr))
		}
		return xrayState{}, fmt.Errorf("%w; rollback completed", err)
	}

	if err := r.applyNetworkTransaction(ctx, result, r.executor); err != nil {
		_ = r.stopCore(core)
		return xrayState{}, err
	}
	if err := r.verifyConnectivity(ctx, r.plan, r.corePlan); err != nil {
		if rollbackErr := r.rollbackTransaction(ctx, transactionID, r.plan, r.executor); rollbackErr != nil {
			_ = r.stopCore(core)
			return xrayState{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after connectivity verification failure: %w", rollbackErr))
		}
		_ = r.stopCore(core)
		return xrayState{}, withTunRollbackCompleted(err)
	}

	active := fullTunnelActiveState(r.profile, r.plan, r.corePlan, transactionID)
	if err := r.commitActiveState(result.Store, transactionID, core, active); err != nil {
		if errors.Is(err, errFullTunnelCoreExitedBeforeCommit) {
			if rollbackErr := r.rollbackTransaction(ctx, transactionID, r.plan, r.executor); rollbackErr != nil {
				return xrayState{}, errors.Join(err, rollbackErr)
			}
			return xrayState{}, fullTunnelSemanticError{msg: "Xray exited before TUN transaction commit; rollback completed", err: errFullTunnelCoreExitedBeforeCommit}
		}
		if rollbackErr := r.rollbackTransaction(ctx, transactionID, r.plan, r.executor); rollbackErr != nil {
			_ = r.stopCore(core)
			return xrayState{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after commit failure: %w", rollbackErr))
		}
		_ = r.stopCore(core)
		return xrayState{}, err
	}

	return active, nil
}

func (r *fullTunnelTransactionRunner) rollbackStarted(ctx context.Context, transactionID, reason string, plan planner.TunPlan, err error) error {
	if rollbackErr := r.rollbackTransaction(ctx, transactionID, plan, r.executor); rollbackErr != nil {
		return errors.Join(err, fmt.Errorf("rollback TUN transaction after %s: %w", reason, rollbackErr))
	}
	return err
}

func (r *fullTunnelTransactionRunner) setDefaults() {
	if r.now == nil {
		r.now = time.Now
	}
	if r.beginNetworkTransaction == nil {
		r.beginNetworkTransaction = beginTunTransaction
	}
	if r.applyNetworkTransaction == nil {
		r.applyNetworkTransaction = applyVerifyTunTransaction
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
	if r.commitActiveState == nil {
		r.commitActiveState = func(store txstate.TransactionStore, transactionID string, _ fullTunnelCoreHandle, _ xrayState) error {
			return commitTunTransaction(store, transactionID)
		}
	}
	if r.rollbackTransaction == nil {
		r.rollbackTransaction = func(ctx context.Context, transactionID string, plan planner.TunPlan, executor tunPlanExecutor) error {
			return rollbackVerifiedTunTransaction(ctx, r.runtimeDir, transactionID, plan, executor)
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
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx, _, err := store.Load(transactionID)
	if err != nil {
		return fmt.Errorf("load TUN transaction %s: %w", transactionID, err)
	}
	return rollbackTunTransaction(ctx, store, &tx, plan, executor)
}

func emptyTunRollbackPlan(plan planner.TunPlan) planner.TunPlan {
	return planner.TunPlan{
		Mode:        plan.Mode,
		TunnelMode:  plan.TunnelMode,
		ProfileID:   plan.ProfileID,
		ProfileName: plan.ProfileName,
	}
}
