package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

var (
	preflightNativeTunSupport          = preflightXrayNativeTunSupport
	validateTunRuntimeDependenciesHook = validateTunRuntimeDependencies
)

type tunCoreRuntimePlan struct {
	RuntimeConfigPath string
	XrayConfig        []byte
	Status            string
	Warnings          []string
	ConnectivityProbe tunConnectivityProbeConfig
}

func (m *XrayManager) connectTun(ctx context.Context, req api.ConnectRequest) (response api.LifecycleResponse, retErr error) {
	p := profileFromSnapshot(req.Profile)
	if err := profile.Validate(p); err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("preflight", "", "not-started", err)
	}
	if err := validateE2ETunHookConfig(); err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("preflight", "", "not-started", err)
	}
	coreIdentity, err := tunCoreExecutionIdentity()
	if err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("core-preflight", "", "not-started", err)
	}

	runtimeDir := m.runtimeDir()
	runtimeConfigPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	xrayPath, err := m.resolveXrayPath()
	if err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("core-preflight", "", "not-started", wrapRuntimeUnavailable("Xray", err))
	}
	if err := validatePackagedRuntimeArchitecture(xrayPath, "Xray"); err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("core-preflight", "", "not-started", err)
	}
	if err := validateTunRuntimeDependenciesHook(); err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("core-preflight", "", "not-started", err)
	}
	if err := preflightNativeTunSupport(ctx, xrayPath, coreIdentity); err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("core-preflight", "", "not-started", err)
	}

	policy := api.NormalizeHandoffPolicy(req.Handoff)
	m.mu.Lock()
	active := m.cmd != nil || m.state.Connection == "active"
	activeMode := m.state.Mode
	m.mu.Unlock()
	if active && (policy != api.HandoffReplacePodlaz || activeMode != planner.ModeTun) {
		return api.LifecycleResponse{}, withTunFailurePhase("handoff", "", "not-started", errConnectionAlreadyActive)
	}

	snapshotOpts := netsnapshot.Options{Server: p.Server}
	snapshot := m.collectTunResourceSnapshot(ctx, snapshotOpts)
	preHandoffPlan, err := planner.PlanTunForSession(p, snapshot, planner.TunOptions{})
	if err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("preflight", "", "not-started", err)
	}
	preHandoffPlan = xrayOwnedTunPlan(preHandoffPlan)
	if _, err := requireTunRuntimeServerBypass(preHandoffPlan); err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("server-bypass", "", "not-started", err)
	}
	if err := requireTunPlanMutationFreePreflight(preHandoffPlan); err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("preflight", "", "not-started", err)
	}
	if err := m.requireTunAddressPreflightBeforeHandoff(ctx, preHandoffPlan, req.Handoff); err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("preflight", "", "not-started", err)
	}
	if err := m.preflightActiveReplacementSessionOwnership(ctx, preHandoffPlan.Snapshot, req.Handoff); err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("handoff", "", "not-started", err)
	}

	var replacementLifecycle *privacyEnvelopeLifecycle
	replacementPrepared := false
	if active && policy == api.HandoffReplacePodlaz {
		candidate := &privacyEnvelopeLifecycle{
			store:    newNetworkSessionStateStore(runtimeDir, nil),
			executor: netexecutor.PrivacyEnvelopeExecutor{},
		}
		state, exists, loadErr := candidate.store.Load()
		if loadErr != nil {
			return api.LifecycleResponse{}, withTunFailurePhase("privacy-envelope-replace", "", "not-started", loadErr)
		}
		if exists && state.Replacement != nil && state.Protection != nil {
			if err := candidate.PrepareReplacement(ctx, preHandoffPlan); err != nil {
				return api.LifecycleResponse{}, withTunFailurePhase("privacy-envelope-replace", "", "not-started", err)
			}
			replacementLifecycle = candidate
			replacementPrepared = true
		}
	}
	defer func() {
		if retErr == nil || !replacementPrepared || replacementLifecycle == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tunRollbackCleanupTimeout)
		defer cancel()
		if cleanupErr := replacementLifecycle.CleanupAfterFailedDataPlane(cleanupCtx); cleanupErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("restore previous protected Network Session: %w", cleanupErr))
		}
	}()

	if err := m.prepareActivePodlazReplace(ctx, req.Handoff); err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("handoff", "", "not-started", err)
	}

	m.mu.Lock()
	if m.cmd != nil || m.state.Connection == "active" {
		m.mu.Unlock()
		return api.LifecycleResponse{}, withTunFailurePhase("handoff", "", "not-started", errConnectionAlreadyActive)
	}
	m.mu.Unlock()

	snapshot = m.collectTunResourceSnapshot(ctx, snapshotOpts)
	snapshot, err = m.autoRecoverTunOwnedState(ctx, snapshot, req.Handoff, snapshotOpts)
	if err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("recovery", "", "not-started", err)
	}
	snapshot, err = m.prepareTunCoexistence(ctx, snapshot, req.Handoff, snapshotOpts)
	if err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("handoff", "", "not-started", err)
	}
	snapshot = m.ensureTunPolicyRuleInventory(ctx, snapshot)
	plan, err := planner.PlanTunForSession(p, snapshot, planner.TunOptions{})
	if err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("preflight", "", "not-started", err)
	}
	plan = xrayOwnedTunPlan(plan)
	if err := requireTunPlanMutationFreePreflight(plan); err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("preflight", "", "not-started", err)
	}
	if err := requireTunAddressPreflight(plan); err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("preflight", "", "not-started", err)
	}
	corePlan, err := planTunCoreRuntime(p, runtimeConfigPath, plan)
	if err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("core-preflight", "", "not-started", err)
	}

	executor := m.tunPlanExecutor()
	runner := fullTunnelTransactionRunner{
		runtimeDir: runtimeDir,
		profile:    p,
		plan:       plan,
		corePlan:   corePlan,
		executor:   executor,
		now:        time.Now,
		startCore: func(context.Context) (fullTunnelCoreHandle, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.cmd != nil || m.state.Connection == "active" {
				return fullTunnelCoreHandle{}, errFullTunnelConnectionBecameActive
			}
			cmd, done, err := m.startXrayLocked(p, xrayPath, corePlan.RuntimeConfigPath, corePlan.XrayConfig, coreIdentity)
			if err != nil {
				return fullTunnelCoreHandle{}, err
			}
			pid := 0
			if cmd.Process != nil {
				pid = cmd.Process.Pid
			}
			return fullTunnelCoreHandle{cmd: cmd, done: done, pid: pid}, nil
		},
		stopCore: func(core fullTunnelCoreHandle) error {
			return m.stopStartedCoreForTransaction(core.cmd, core.done)
		},
		collectFailureDiagnostics: func(ctx context.Context, transactionID string, plan planner.TunPlan, cause error) tunFailureDiagnosticSummary {
			return m.collectTunFailureDiagnostics(ctx, transactionID, plan, cause)
		},
		finalizeFailureDiagnostics: func(ctx context.Context, summary tunFailureDiagnosticSummary, status string) {
			m.finalizeTunFailureDiagnosticRollback(ctx, summary, status)
		},
		commitActiveState: func(store txstate.TransactionStore, transactionID string, core fullTunnelCoreHandle, active xrayState) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.cmd != core.cmd || m.done != core.done {
				return errFullTunnelCoreExitedBeforeCommit
			}
			if err := commitTunTransaction(store, transactionID); err != nil {
				return err
			}
			if replacementPrepared && replacementLifecycle != nil {
				if err := replacementLifecycle.store.CommitReplacement(); err != nil {
					return fmt.Errorf("commit protected Network Session replacement: %w", err)
				}
			}
			m.state = active
			return nil
		},
	}
	if err := m.configurePrivacyEnvelope(&runner); err != nil {
		return api.LifecycleResponse{}, withTunFailurePhase("privacy-envelope-preflight", "", "not-started", err)
	}
	activeState, err := runner.run(ctx)
	if err != nil {
		return api.LifecycleResponse{}, err
	}
	return lifecycleResponse(activeState), nil
}

func (m *XrayManager) disconnectTun(ctx context.Context, transactionID string) (api.LifecycleResponse, error) {
	return m.runTunCleanup(ctx, transactionID)
}
