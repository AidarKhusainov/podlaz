package daemon

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/engine"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
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
	if err := m.prepareActivePodlazReplace(ctx, req.Handoff); err != nil {
		return api.LifecycleResponse{}, err
	}

	m.mu.Lock()
	if m.cmd != nil || m.state.Connection == "active" {
		m.mu.Unlock()
		return api.LifecycleResponse{}, errConnectionAlreadyActive
	}
	m.mu.Unlock()

	runtimeDir := m.runtimeDir()
	runtimeConfigPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	xrayPath, err := m.resolveXrayPath()
	if err != nil {
		return api.LifecycleResponse{}, wrapRuntimeUnavailable("Xray", err)
	}
	if err := validatePackagedRuntimeArchitecture(xrayPath, "Xray"); err != nil {
		return api.LifecycleResponse{}, err
	}
	if err := validateTunRuntimeDependencies(); err != nil {
		return api.LifecycleResponse{}, err
	}

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
		preflightCore: func(ctx context.Context) error {
			return preflightXrayNativeTunSupport(ctx, xrayPath, coreIdentity)
		},
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

func planTunCoreRuntime(p profile.Profile, runtimeConfigPath string, plan planner.TunPlan) (tunCoreRuntimePlan, error) {
	if runtimeConfigPath == "" {
		return tunCoreRuntimePlan{}, errors.New("TUN-mode Xray runtime config requires a runtime config path")
	}
	opts := engine.DefaultXrayTunConfigOptions()
	opts.Name = plan.TunDevice.Name
	opts.MTU = plan.TunDevice.MTU
	if serverIP := tunRuntimeServerAddress(plan); serverIP != "" {
		opts.OutboundAddressOverride = serverIP
	}
	xrayConfig, err := engine.GenerateXrayTunConfig(p, opts)
	if err != nil {
		return tunCoreRuntimePlan{}, err
	}
	warnings := []string{
		"TUN-mode connectivity is verified through full-tunnel route lookup, routed TCP probe, and DNS probe before transaction commit",
		"Pinned Xray TUN schema owns packet ingestion only; podlazd owns Linux route and DNS state and fails before commit if route, TCP, or DNS verification does not pass",
		"Xray owns podlaz0 lifecycle; podlazd verifies the link and owns routes, DNS, nftables, rollback, and recovery metadata",
	}
	if opts.OutboundAddressOverride != "" && opts.OutboundAddressOverride != p.Server {
		warnings = append(warnings, "TUN-mode Xray runtime uses the pre-resolved VPN server IP to avoid recursive DNS through the full-tunnel route")
	}
	return tunCoreRuntimePlan{
		RuntimeConfigPath: runtimeConfigPath,
		XrayConfig:        xrayConfig,
		Status:            "TUN-mode Xray runtime config with native podlaz0 TUN inbound",
		Warnings:          warnings,
	}, nil
}

func xrayOwnedTunPlan(plan planner.TunPlan) planner.TunPlan {
	plan.TunDevice.Action = "verify"
	plan.TunDevice.Reason = "Xray tun inbound owns podlaz0 creation and packet ingestion; podlaz verifies the link before applying routes, DNS, and firewall state"
	plan.Steps = xrayOwnedTunSteps(plan)
	plan.RollbackSteps = xrayOwnedTunRollbackSteps(plan)
	return plan
}

func xrayOwnedTunSteps(plan planner.TunPlan) []string {
	steps := []string{"Start Xray native tun inbound and verify podlaz0 before applying podlaz-owned Linux routes, DNS, and nftables state"}
	for _, step := range plan.Steps {
		if strings.Contains(step, "Plan TUN interface") || strings.Contains(step, "Leave TUN devices") {
			continue
		}
		steps = append(steps, step)
	}
	return steps
}

func xrayOwnedTunRollbackSteps(plan planner.TunPlan) []string {
	steps := []string{"Roll back podlaz-owned nftables, DNS, routes, and policy rules before stopping Xray and releasing podlaz0"}
	for _, step := range plan.RollbackSteps {
		if strings.Contains(step, "Delete TUN interface") {
			continue
		}
		steps = append(steps, step)
	}
	return steps
}

func tunRuntimeServerAddress(plan planner.TunPlan) string {
	serverBypass := strings.TrimSpace(plan.ServerBypass.Destination)
	if serverBypass == "" || serverBypass == "<server-ip>" {
		return ""
	}
	ip, _, err := net.ParseCIDR(serverBypass)
	if err == nil && ip.To4() != nil {
		return ip.String()
	}
	if parsed := net.ParseIP(serverBypass); parsed != nil && parsed.To4() != nil {
		return parsed.String()
	}
	return ""
}

func (m *XrayManager) disconnectTun(ctx context.Context, transactionID string) (api.LifecycleResponse, error) {
	return m.runTunCleanup(ctx, transactionID)
}
