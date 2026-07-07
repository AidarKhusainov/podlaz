package daemon

import (
	"context"
	"path/filepath"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
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
}

func (m *XrayManager) connectTun(ctx context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
	p := profileFromSnapshot(req.Profile)
	if err := profile.Validate(p); err != nil {
		return api.LifecycleResponse{}, err
	}
	if err := validateE2ETunHookConfig(); err != nil {
		return api.LifecycleResponse{}, err
	}
	coreIdentity, err := tunCoreExecutionIdentity()
	if err != nil {
		return api.LifecycleResponse{}, err
	}

	runtimeDir := m.runtimeDir()
	runtimeConfigPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	xrayPath, err := m.resolveXrayPath()
	if err != nil {
		return api.LifecycleResponse{}, wrapRuntimeUnavailable("Xray", err)
	}
	if err := validatePackagedRuntimeArchitecture(xrayPath, "Xray"); err != nil {
		return api.LifecycleResponse{}, err
	}
	if err := validateTunRuntimeDependenciesHook(); err != nil {
		return api.LifecycleResponse{}, err
	}
	if err := preflightNativeTunSupport(ctx, xrayPath, coreIdentity); err != nil {
		return api.LifecycleResponse{}, err
	}

	if err := m.prepareActivePodlazReplace(ctx, req.Handoff); err != nil {
		return api.LifecycleResponse{}, err
	}

	m.mu.Lock()
	if m.cmd != nil || m.state.Connection == "active" {
		m.mu.Unlock()
		return api.LifecycleResponse{}, errConnectionAlreadyActive
	}
	m.mu.Unlock()

	snapshotOpts := netsnapshot.Options{Server: p.Server}
	snapshot := m.collectTunSnapshot(ctx, snapshotOpts)
	snapshot, err = m.prepareTunHandoff(ctx, snapshot, req.Handoff, snapshotOpts)
	if err != nil {
		return api.LifecycleResponse{}, err
	}
	plan, err := planner.PlanTun(p, snapshot)
	if err != nil {
		return api.LifecycleResponse{}, err
	}
	plan = xrayOwnedTunPlan(plan)
	corePlan, err := planTunCoreRuntime(p, runtimeConfigPath, plan)
	if err != nil {
		return api.LifecycleResponse{}, err
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
			return m.stopStartedCore(core.cmd, core.done, corePlan.RuntimeConfigPath)
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
			m.state = active
			return nil
		},
	}
	active, err := runner.run(ctx)
	if err != nil {
		return api.LifecycleResponse{}, err
	}
	return lifecycleResponse(active), nil
}

func (m *XrayManager) disconnectTun(ctx context.Context, transactionID string) (api.LifecycleResponse, error) {
	return m.runTunCleanup(ctx, transactionID)
}
