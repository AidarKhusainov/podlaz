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

func (e fullTunnelSemanticError) Error() string {
	return e.msg
}

func (e fullTunnelSemanticError) Unwrap() error {
	return e.err
}

type fullTunnelCoreHandle struct {
	cmd  *exec.Cmd
	done <-chan struct{}
	pid  int
}

type fullTunnelAdapterHandle struct {
	pid int
}

type fullTunnelTransactionRunner struct {
	runtimeDir string
	profile    profile.Profile
	plan       planner.TunPlan
	corePlan   tunCoreRuntimePlan
	executor   tunPlanExecutor
	now        func() time.Time

	runNetworkTransaction func(context.Context, string, profile.Profile, planner.TunPlan, tunPlanExecutor, func() time.Time) (tunTransactionResult, error)
	startCore             func(context.Context) (fullTunnelCoreHandle, error)
	stopCore              func(fullTunnelCoreHandle) error
	verifyCoreStarted     func(<-chan struct{}) error
	saveCoreMetadata      func(txstate.TransactionStore, string, string, int, time.Time) error
	startAdapter          func(context.Context, tunAdapterRuntimePlan) (fullTunnelAdapterHandle, error)
	stopAdapter           func() error
	saveAdapterMetadata   func(txstate.TransactionStore, string, int, time.Time) error
	verifyConnectivity    func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error
	commitActiveState     func(txstate.TransactionStore, string, fullTunnelCoreHandle, xrayState) error
	rollbackTransaction   func(context.Context, string, planner.TunPlan, tunPlanExecutor) error
}

func (r *fullTunnelTransactionRunner) run(ctx context.Context) (xrayState, error) {
	r.setDefaults()

	result, err := r.runNetworkTransaction(ctx, r.runtimeDir, r.profile, r.plan, r.executor, r.now)
	if err != nil {
		return xrayState{}, err
	}

	core, err := r.startCore(ctx)
	if err != nil {
		if rollbackErr := r.rollbackTransaction(ctx, result.TransactionID, r.plan, r.executor); rollbackErr != nil {
			if errors.Is(err, errFullTunnelConnectionBecameActive) {
				return xrayState{}, errors.Join(err, rollbackErr)
			}
			return xrayState{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after Xray start failure: %w", rollbackErr))
		}
		if errors.Is(err, errFullTunnelConnectionBecameActive) {
			return xrayState{}, fullTunnelSemanticError{
				msg: "connection already active; rolled back newly applied TUN transaction",
				err: errFullTunnelConnectionBecameActive,
			}
		}
		return xrayState{}, err
	}

	if err := r.saveCoreMetadata(result.Store, result.TransactionID, r.corePlan.RuntimeConfigPath, core.pid, transactionNow(result.Store)); err != nil {
		_ = r.stopCore(core)
		if rollbackErr := r.rollbackTransaction(ctx, result.TransactionID, r.plan, r.executor); rollbackErr != nil {
			return xrayState{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after core metadata failure: %w", rollbackErr))
		}
		return xrayState{}, err
	}
	if err := r.verifyCoreStarted(core.done); err != nil {
		_ = r.stopCore(core)
		if rollbackErr := r.rollbackTransaction(ctx, result.TransactionID, r.plan, r.executor); rollbackErr != nil {
			return xrayState{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after Xray startup verification failure: %w", rollbackErr))
		}
		return xrayState{}, fmt.Errorf("%w; rolled back applied podlaz-owned networking state", err)
	}

	adapter, err := r.startAdapter(ctx, tunAdapterRuntimePlan{TunDevice: r.plan.TunDevice.Name, SOCKSEndpoint: r.corePlan.SOCKSEndpoint})
	if err != nil {
		_ = r.stopCore(core)
		if rollbackErr := r.rollbackTransaction(ctx, result.TransactionID, r.plan, r.executor); rollbackErr != nil {
			return xrayState{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after TUN adapter startup failure: %w", rollbackErr))
		}
		return xrayState{}, err
	}
	if err := r.saveAdapterMetadata(result.Store, result.TransactionID, adapter.pid, transactionNow(result.Store)); err != nil {
		_ = r.stopAdapter()
		_ = r.stopCore(core)
		if rollbackErr := r.rollbackTransaction(ctx, result.TransactionID, r.plan, r.executor); rollbackErr != nil {
			return xrayState{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after TUN adapter metadata failure: %w", rollbackErr))
		}
		return xrayState{}, err
	}
	if err := r.verifyConnectivity(ctx, r.plan, r.corePlan); err != nil {
		_ = r.stopAdapter()
		_ = r.stopCore(core)
		if rollbackErr := r.rollbackTransaction(ctx, result.TransactionID, r.plan, r.executor); rollbackErr != nil {
			return xrayState{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after connectivity verification failure: %w", rollbackErr))
		}
		return xrayState{}, fmt.Errorf("%w; rolled back applied podlaz-owned networking state", err)
	}

	active := fullTunnelActiveState(r.profile, r.plan, r.corePlan, result.TransactionID)
	if err := r.commitActiveState(result.Store, result.TransactionID, core, active); err != nil {
		if errors.Is(err, errFullTunnelCoreExitedBeforeCommit) {
			_ = r.stopAdapter()
			if rollbackErr := r.rollbackTransaction(ctx, result.TransactionID, r.plan, r.executor); rollbackErr != nil {
				return xrayState{}, errors.Join(err, rollbackErr)
			}
			return xrayState{}, fullTunnelSemanticError{
				msg: "Xray exited before TUN transaction commit; rolled back applied podlaz-owned networking state",
				err: errFullTunnelCoreExitedBeforeCommit,
			}
		}
		_ = r.stopAdapter()
		_ = r.stopCore(core)
		if rollbackErr := r.rollbackTransaction(ctx, result.TransactionID, r.plan, r.executor); rollbackErr != nil {
			return xrayState{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after commit failure: %w", rollbackErr))
		}
		return xrayState{}, err
	}

	return active, nil
}

func (r *fullTunnelTransactionRunner) setDefaults() {
	if r.now == nil {
		r.now = time.Now
	}
	if r.runNetworkTransaction == nil {
		r.runNetworkTransaction = runTunTransaction
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
	if r.startAdapter == nil {
		r.startAdapter = func(context.Context, tunAdapterRuntimePlan) (fullTunnelAdapterHandle, error) {
			return fullTunnelAdapterHandle{}, errors.New("missing full-tunnel TUN adapter starter")
		}
	}
	if r.stopAdapter == nil {
		r.stopAdapter = func() error { return nil }
	}
	if r.saveAdapterMetadata == nil {
		r.saveAdapterMetadata = saveTunAdapterRollbackMetadata
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
