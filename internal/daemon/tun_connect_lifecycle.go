package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"time"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	"github.com/AidarKhusainov/tunwarden/internal/engine"
	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/tunwarden/internal/network/snapshot"
	"github.com/AidarKhusainov/tunwarden/internal/profile"
	txstate "github.com/AidarKhusainov/tunwarden/internal/state"
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
	if err := ensureCoreNotRoot(planner.ModeTun); err != nil {
		return api.LifecycleResponse{}, err
	}

	runtimeDir := m.runtimeDir()
	runtimeConfigPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	corePlan, err := planTunCoreRuntime(p, runtimeConfigPath)
	if err != nil {
		return api.LifecycleResponse{}, err
	}
	xrayPath, err := m.resolveXrayPath()
	if err != nil {
		return api.LifecycleResponse{}, err
	}

	m.mu.Lock()
	if m.cmd != nil || m.state.Connection == "active" {
		m.mu.Unlock()
		return api.LifecycleResponse{}, errors.New("connection already active; run tunwarden disconnect before connecting another profile")
	}
	m.mu.Unlock()

	snapshot := m.collectTunSnapshot(ctx, netsnapshot.Options{Server: p.Server})
	plan, err := planner.PlanTun(p, snapshot)
	if err != nil {
		return api.LifecycleResponse{}, err
	}

	executor := m.tunPlanExecutor()
	result, err := runTunTransaction(ctx, runtimeDir, p, plan, executor, time.Now)
	if err != nil {
		return api.LifecycleResponse{}, err
	}

	m.mu.Lock()
	if m.cmd != nil || m.state.Connection == "active" {
		m.mu.Unlock()
		if rollbackErr := m.rollbackVerifiedTun(ctx, result.TransactionID, plan, executor); rollbackErr != nil {
			return api.LifecycleResponse{}, errors.Join(errors.New("connection became active while TUN transaction was applying"), rollbackErr)
		}
		return api.LifecycleResponse{}, errors.New("connection already active; rolled back newly applied TUN transaction")
	}
	cmd, done, err := m.startXrayLocked(p, xrayPath, corePlan.RuntimeConfigPath, corePlan.XrayConfig)
	if err != nil {
		m.mu.Unlock()
		if rollbackErr := m.rollbackVerifiedTun(ctx, result.TransactionID, plan, executor); rollbackErr != nil {
			return api.LifecycleResponse{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after Xray start failure: %w", rollbackErr))
		}
		return api.LifecycleResponse{}, err
	}
	m.mu.Unlock()

	if err := saveCoreRollbackMetadata(result.Store, result.TransactionID, corePlan.RuntimeConfigPath, cmd.Process.Pid, transactionNow(result.Store)); err != nil {
		_ = m.stopStartedCore(cmd, done, corePlan.RuntimeConfigPath)
		if rollbackErr := m.rollbackVerifiedTun(ctx, result.TransactionID, plan, executor); rollbackErr != nil {
			return api.LifecycleResponse{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after core metadata failure: %w", rollbackErr))
		}
		return api.LifecycleResponse{}, err
	}
	if err := verifyCoreStarted(done); err != nil {
		_ = m.stopStartedCore(cmd, done, corePlan.RuntimeConfigPath)
		if rollbackErr := m.rollbackVerifiedTun(ctx, result.TransactionID, plan, executor); rollbackErr != nil {
			return api.LifecycleResponse{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after Xray startup verification failure: %w", rollbackErr))
		}
		return api.LifecycleResponse{}, fmt.Errorf("%w; rolled back applied TunWarden-owned networking state", err)
	}
	if err := verifyTunConnectivity(ctx, plan, corePlan); err != nil {
		_ = m.stopStartedCore(cmd, done, corePlan.RuntimeConfigPath)
		if rollbackErr := m.rollbackVerifiedTun(ctx, result.TransactionID, plan, executor); rollbackErr != nil {
			return api.LifecycleResponse{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after connectivity verification failure: %w", rollbackErr))
		}
		return api.LifecycleResponse{}, fmt.Errorf("%w; rolled back applied TunWarden-owned networking state", err)
	}

	active := xrayState{
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
		TransactionID:     result.TransactionID,
		Warnings:          append(append([]string{}, corePlan.Warnings...), plan.Warnings...),
	}
	m.mu.Lock()
	if m.cmd != cmd || m.done != done {
		m.mu.Unlock()
		if rollbackErr := m.rollbackVerifiedTun(ctx, result.TransactionID, plan, executor); rollbackErr != nil {
			return api.LifecycleResponse{}, errors.Join(errors.New("Xray exited before TUN transaction commit"), rollbackErr)
		}
		return api.LifecycleResponse{}, errors.New("Xray exited before TUN transaction commit; rolled back applied TunWarden-owned networking state")
	}
	if err := commitTunTransaction(result.Store, result.TransactionID); err != nil {
		m.mu.Unlock()
		_ = m.stopStartedCore(cmd, done, corePlan.RuntimeConfigPath)
		if rollbackErr := m.rollbackVerifiedTun(ctx, result.TransactionID, plan, executor); rollbackErr != nil {
			return api.LifecycleResponse{}, errors.Join(err, fmt.Errorf("rollback TUN transaction after commit failure: %w", rollbackErr))
		}
		return api.LifecycleResponse{}, err
	}
	m.state = active
	m.mu.Unlock()
	return lifecycleResponse(active), nil
}

func planTunCoreRuntime(p profile.Profile, runtimeConfigPath string) (tunCoreRuntimePlan, error) {
	if runtimeConfigPath == "" {
		return tunCoreRuntimePlan{}, errors.New("TUN-mode Xray runtime config requires a runtime config path")
	}
	opts := engine.DefaultXrayTunConfigOptions()
	xrayConfig, err := engine.GenerateXrayTunConfig(p, opts)
	if err != nil {
		return tunCoreRuntimePlan{}, err
	}
	endpoint := net.JoinHostPort(opts.SOCKSListen, fmt.Sprintf("%d", opts.SOCKSPort))
	return tunCoreRuntimePlan{
		RuntimeConfigPath: runtimeConfigPath,
		XrayConfig:        xrayConfig,
		SOCKSEndpoint:     endpoint,
		Status:            "TUN-mode Xray runtime config with private SOCKS adapter endpoint " + endpoint,
		Warnings:          []string{"TUN-mode connectivity is verified through the daemon-private Xray SOCKS endpoint before transaction commit"},
	}, nil
}

func (m *XrayManager) disconnectTun(ctx context.Context, transactionID string) (api.LifecycleResponse, error) {
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
	store := txstate.TransactionStore{RuntimeDir: m.runtimeDir()}
	tx, _, err := store.Load(transactionID)
	if err != nil {
		return fmt.Errorf("load TUN transaction %s: %w", transactionID, err)
	}
	return rollbackTunTransaction(ctx, store, &tx, plan, executor)
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
