package daemon

import (
	"context"
	"errors"
	"fmt"
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
	SOCKSEndpoint     string
	Status            string
	Warnings          []string
}

func (m *XrayManager) connectTun(ctx context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
	p := profileFromSnapshot(req.Profile)
	if err := profile.Validate(p); err != nil {
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
		return api.LifecycleResponse{}, err
	}

	m.mu.Lock()
	if m.cmd != nil || m.state.Connection == "active" {
		m.mu.Unlock()
		return api.LifecycleResponse{}, errConnectionAlreadyActive
	}
	m.mu.Unlock()

	snapshot := m.collectTunSnapshot(ctx, netsnapshot.Options{Server: p.Server})
	plan, err := planner.PlanTun(p, snapshot)
	if err != nil {
		return api.LifecycleResponse{}, err
	}
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
		startAdapter: func(ctx context.Context, plan tunAdapterRuntimePlan) (fullTunnelAdapterHandle, error) {
			plan.Identity = coreIdentity
			adapterCmd, adapterDone, adapterCancel, err := startTunAdapter(ctx, plan)
			if err != nil {
				return fullTunnelAdapterHandle{}, err
			}
			registerTunAdapter(m, adapterCancel, adapterDone)
			pid := 0
			if adapterCmd.Process != nil {
				pid = adapterCmd.Process.Pid
			}
			return fullTunnelAdapterHandle{pid: pid}, nil
		},
		stopAdapter: func() error {
			return stopRegisteredTunAdapter(m)
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
	if serverIP := tunRuntimeServerAddress(plan); serverIP != "" {
		opts.OutboundAddressOverride = serverIP
	}
	xrayConfig, err := engine.GenerateXrayTunConfig(p, opts)
	if err != nil {
		return tunCoreRuntimePlan{}, err
	}
	endpoint := net.JoinHostPort(opts.SOCKSListen, fmt.Sprintf("%d", opts.SOCKSPort))
	warnings := []string{"TUN-mode connectivity is verified through full-tunnel route lookup, routed TCP probe, and DNS probe before transaction commit"}
	if opts.OutboundAddressOverride != "" && opts.OutboundAddressOverride != p.Server {
		warnings = append(warnings, "TUN-mode Xray runtime uses the pre-resolved VPN server IP to avoid recursive DNS through the full-tunnel route")
	}
	return tunCoreRuntimePlan{
		RuntimeConfigPath: runtimeConfigPath,
		XrayConfig:        xrayConfig,
		SOCKSEndpoint:     endpoint,
		Status:            "TUN-mode Xray runtime config with private adapter SOCKS endpoint " + endpoint,
		Warnings:          warnings,
	}, nil
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
	if err := stopRegisteredTunAdapter(m); err != nil {
		return api.LifecycleResponse{}, err
	}
	store := txstate.TransactionStore{RuntimeDir: m.runtimeDir()}
	tx, _, err := store.Load(transactionID)
	if err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("load TUN transaction %s: %w", transactionID, err)
	}
	plan := tunPlanFromTransaction(tx)
	if err := rollbackTunTransaction(ctx, store, &tx, plan, m.tunPlanExecutor()); err != nil {
		return api.LifecycleResponse{}, err
	}
	if err := removeTransactionFile(store, transactionID); err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("remove rolled-back TUN transaction %s: %w", transactionID, err)
	}
	m.mu.Lock()
	m.state = inactiveXrayState()
	m.mu.Unlock()
	return lifecycleResponse(inactiveXrayState()), nil
}

func (m *XrayManager) rollbackVerifiedTun(ctx context.Context, transactionID string, plan planner.TunPlan, executor tunPlanExecutor) error {
	return rollbackVerifiedTunTransaction(ctx, m.runtimeDir(), transactionID, plan, executor)
}

func verifyCoreStarted(done <-chan struct{}) error {
	if done == nil {
		return errors.New("missing Xray process completion channel")
	}
	select {
	case <-done:
		return errors.New("Xray exited during startup verification")
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}
