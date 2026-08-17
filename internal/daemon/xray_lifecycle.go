package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/doctor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

const (
	defaultStopTimeout = 5 * time.Second
	generatedDirName   = "generated"
	generatedXrayName  = "xray.json"
)

// XrayManager owns daemon-side connection lifecycle.
type XrayManager struct {
	RuntimeDir  string
	XrayPath    string
	StopTimeout time.Duration

	tunExecutor       tunPlanExecutor
	snapshotCollector tunSnapshotCollector

	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{}
	stopping bool
	state    xrayState
}

type xrayState struct {
	Connection        string
	Mode              string
	ProfileID         string
	ProfileName       string
	Proxy             string
	TUN               string
	Routes            string
	DNS               string
	Firewall          string
	RuntimeConfigPath string
	TransactionID     string
	Warnings          []string
}

func NewXrayManager(runtimeDir string) *XrayManager {
	return &XrayManager{RuntimeDir: runtimeDir}
}

func (m *XrayManager) Connect(ctx context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
	if err := api.ValidateConnectRequest(req); err != nil {
		return api.LifecycleResponse{}, err
	}
	switch strings.TrimSpace(req.Mode) {
	case planner.ModeProxyOnly:
		return m.connectProxyOnly(ctx, req)
	case planner.ModeTun:
		return m.connectTun(ctx, req)
	default:
		return api.LifecycleResponse{}, fmt.Errorf("unsupported connect mode %q", req.Mode)
	}
}

func (m *XrayManager) connectProxyOnly(ctx context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
	_ = ctx
	p := profileFromSnapshot(req.Profile)
	if err := profile.Validate(p); err != nil {
		return api.LifecycleResponse{}, err
	}
	coreIdentity, err := proxyOnlyCoreExecutionIdentity()
	if err != nil {
		return api.LifecycleResponse{}, err
	}

	runtimeDir := m.runtimeDir()
	runtimeConfigPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	proxyPlan, err := planner.PlanProxyOnlyWithOptions(p, planner.ProxyOnlyOptions{RuntimeConfigPath: runtimeConfigPath})
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
		return api.LifecycleResponse{}, errConnectionAlreadyActive
	}
	if _, _, err := m.startXrayLocked(p, xrayPath, runtimeConfigPath, proxyPlan.XrayConfig, coreIdentity); err != nil {
		m.mu.Unlock()
		return api.LifecycleResponse{}, err
	}

	active := xrayState{
		Connection:        "active",
		Mode:              planner.ModeProxyOnly,
		ProfileID:         p.ID,
		ProfileName:       p.Name,
		Proxy:             proxyListenersLine(proxyPlan.Listeners),
		TUN:               "disabled",
		Routes:            "not modified",
		DNS:               "not modified",
		Firewall:          "not modified",
		RuntimeConfigPath: runtimeConfigPath,
		Warnings:          proxyPlan.Warnings,
	}

	m.state = active
	m.mu.Unlock()
	return lifecycleResponse(active), nil
}

func (m *XrayManager) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	m.mu.Lock()
	cmd := m.cmd
	done := m.done
	configPath := m.state.RuntimeConfigPath
	mode := m.state.Mode
	transactionID := m.state.TransactionID
	if cmd == nil {
		if m.state.Connection == "active" && m.state.Mode == planner.ModeTun {
			m.mu.Unlock()
			if transactionID == "" {
				return api.LifecycleResponse{}, errors.New("active TUN connection has no transaction id; run podlaz recover")
			}
			return m.disconnectTun(ctx, transactionID)
		}
		m.state = inactiveXrayState()
		m.mu.Unlock()
		removeGeneratedConfig(configPath)
		return lifecycleResponse(inactiveXrayState()), nil
	}
	if mode == planner.ModeTun {
		m.mu.Unlock()
		if transactionID == "" {
			return api.LifecycleResponse{}, errors.New("active TUN connection has no transaction id; run podlaz recover")
		}
		return m.disconnectActiveTun(ctx, transactionID, cmd, done, configPath)
	}
	m.stopping = true
	m.mu.Unlock()

	if err := m.stopCoreProcess(cmd, done); err != nil {
		return api.LifecycleResponse{}, err
	}
	removeGeneratedConfig(configPath)

	return lifecycleResponse(inactiveXrayState()), nil
}

func (m *XrayManager) disconnectActiveTun(ctx context.Context, transactionID string, cmd *exec.Cmd, done <-chan struct{}, configPath string) (api.LifecycleResponse, error) {
	store := txstate.TransactionStore{RuntimeDir: m.runtimeDir()}
	tx, _, err := store.Load(transactionID)
	if err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("load active TUN transaction %s: %w", transactionID, err)
	}
	projection := recovery.ProjectRollbackMetadata(tx)
	if projection.Incomplete {
		return api.LifecycleResponse{}, fmt.Errorf("TUN rollback authority incomplete: %s", strings.Join(projection.Reasons, "; "))
	}
	projectedTx := tx
	projectedTx.Rollback = projection.Rollback
	plan := tunPlanFromTransaction(projectedTx)
	if err := beginTunRollback(store, &tx); err != nil {
		return api.LifecycleResponse{}, err
	}
	if err := rollbackTunHostStateForTransaction(ctx, plan, m.tunPlanExecutor(), tx); err != nil {
		_, _ = txstate.MarkFailure(&tx, err.Error(), transactionNow(store))
		_, _ = store.Save(tx)
		return api.LifecycleResponse{}, fmt.Errorf("rollback active TUN host networking before stopping Xray: %w", err)
	}

	m.mu.Lock()
	if m.cmd == cmd {
		m.stopping = true
	}
	m.mu.Unlock()
	if err := m.stopCoreProcess(cmd, done); err != nil {
		_, _ = txstate.MarkFailure(&tx, err.Error(), transactionNow(store))
		_, _ = store.Save(tx)
		return api.LifecycleResponse{}, fmt.Errorf("stop Xray after active TUN host rollback: %w", err)
	}

	removeRollbackGeneratedConfigs(tx)
	removeGeneratedConfig(configPath)
	if err := finishTunRollback(store, &tx); err != nil {
		return api.LifecycleResponse{}, err
	}
	if err := removeTransactionFile(store, transactionID); err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("remove rolled-back TUN transaction %s: %w", transactionID, err)
	}

	m.mu.Lock()
	if m.cmd == cmd {
		m.cmd = nil
		m.done = nil
	}
	m.stopping = false
	m.state = inactiveXrayState()
	m.mu.Unlock()
	return lifecycleResponse(inactiveXrayState()), nil
}

func (m *XrayManager) Status(context.Context) api.StatusResponse {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state
	if state.Connection == "" {
		state = inactiveXrayState()
	}
	transactions, inspectionWarnings := transactionStatuses(m.runtimeDir())
	return api.StatusResponse{
		Daemon:             "running",
		Service:            api.ServiceFromEnv(),
		Connection:         state.Connection,
		Mode:               state.Mode,
		ProfileID:          state.ProfileID,
		ProfileName:        state.ProfileName,
		RuntimeDirectory:   "present",
		RuntimeConfigPath:  state.RuntimeConfigPath,
		Proxy:              state.Proxy,
		TUN:                state.TUN,
		Routes:             state.Routes,
		DNS:                state.DNS,
		Firewall:           state.Firewall,
		Transactions:       transactions,
		Warnings:           append([]string(nil), state.Warnings...),
		InspectionWarnings: inspectionWarnings,
	}
}

func (m *XrayManager) Doctor(ctx context.Context) api.DoctorResponse {
	return m.doctorFromSnapshot(ctx, m.captureDoctorLifecycleSnapshot())
}

func (m *XrayManager) lifecycleDoctorChecks(ctx context.Context) []doctor.Check {
	return m.lifecycleDoctorChecksFromSnapshot(ctx, m.captureDoctorLifecycleSnapshot())
}

func (m *XrayManager) waitForExit(cmd *exec.Cmd, done chan struct{}, coreLogs []*coreLogWriter, runtimeConfigPath string) {
	err := cmd.Wait()
	for _, coreLog := range coreLogs {
		coreLog.Flush()
	}
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	defer close(done)

	if m.cmd != cmd {
		return
	}
	m.cmd = nil
	m.done = nil
	if m.stopping {
		m.stopping = false
		m.state = inactiveXrayState()
		removeGeneratedConfig(runtimeConfigPath)
		logCoreStopped(pid)
		return
	}
	exitMessage := processExitMessage(err)
	m.state.Connection = "error (core exited)"
	m.state.Proxy = "inactive"
	m.state.Warnings = append(m.state.Warnings, exitMessage)
	logCoreExited(pid)
}

func (m *XrayManager) runtimeDir() string {
	if m.RuntimeDir != "" {
		return m.RuntimeDir
	}
	return api.RuntimeDirFromEnv()
}

func transactionStatuses(runtimeDir string) ([]api.TransactionStatus, []api.RecoveryWarning) {
	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	statuses := make([]api.TransactionStatus, 0, len(summaries))
	for _, summary := range summaries {
		statuses = append(statuses, api.TransactionStatus{
			ID:                summary.ID,
			State:             string(summary.State),
			RollbackAvailable: summary.RollbackAvailable,
			RequiresCleanup:   summary.RequiresCleanup,
			Path:              summary.Path,
		})
	}
	inspectionWarnings := make([]api.RecoveryWarning, 0, len(warnings))
	for _, warning := range warnings {
		inspectionWarnings = append(inspectionWarnings, api.RecoveryWarning{Target: "transaction state", Message: warning})
	}
	return statuses, inspectionWarnings
}

func (m *XrayManager) resolveXrayPath() (string, error) {
	xrayPath := strings.TrimSpace(m.XrayPath)
	if xrayPath == "" {
		xrayPath = strings.TrimSpace(os.Getenv(api.XrayPathEnv))
	}
	if xrayPath == "" {
		xrayPath = api.DefaultXrayCommand
	}
	if strings.ContainsRune(xrayPath, os.PathSeparator) {
		info, err := os.Stat(xrayPath)
		if err != nil {
			return "", fmt.Errorf("resolve Xray binary %s: %w", xrayPath, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("resolve Xray binary %s: is a directory", xrayPath)
		}
		if info.Mode().Perm()&0o111 == 0 {
			return "", fmt.Errorf("resolve Xray binary %s: not executable", xrayPath)
		}
		return xrayPath, nil
	}
	path, err := exec.LookPath(xrayPath)
	if err != nil {
		return "", fmt.Errorf("resolve Xray binary %q: %w; set %s to the Xray executable path", xrayPath, err, api.XrayPathEnv)
	}
	return path, nil
}

func (m *XrayManager) startXrayLocked(p profile.Profile, xrayPath, runtimeConfigPath string, xrayConfig []byte, identities ...coreExecutionIdentity) (*exec.Cmd, chan struct{}, error) {
	identity := sameUserCoreExecutionIdentity()
	if len(identities) > 0 {
		identity = identities[0]
	}
	if err := writeRuntimeConfig(runtimeConfigPath, xrayConfig, identity.runtimeConfigPermissions()); err != nil {
		return nil, nil, err
	}

	cmd := exec.Command(xrayPath, "run", "-config", runtimeConfigPath)
	stdoutLog := newCoreLogWriter("stdout")
	stderrLog := newCoreLogWriter("stderr")
	cmd.Stdout = stdoutLog
	cmd.Stderr = stderrLog
	configureCoreCommandCredential(cmd, identity)
	if err := cmd.Start(); err != nil {
		removeGeneratedConfig(runtimeConfigPath)
		logCoreStartFailed()
		return nil, nil, fmt.Errorf("start Xray: %w", err)
	}

	pid := cmd.Process.Pid
	stdoutLog.setPID(pid)
	stderrLog.setPID(pid)
	logCoreStarted(pid)

	done := make(chan struct{})
	m.cmd = cmd
	m.done = done
	m.stopping = false
	go m.waitForExit(cmd, done, []*coreLogWriter{stdoutLog, stderrLog}, runtimeConfigPath)
	return cmd, done, nil
}

func (m *XrayManager) stopStartedCore(cmd *exec.Cmd, done <-chan struct{}, runtimeConfigPath string) error {
	m.mu.Lock()
	if m.cmd == cmd {
		m.stopping = true
	}
	m.mu.Unlock()
	err := m.stopCoreProcess(cmd, done)
	removeGeneratedConfig(runtimeConfigPath)
	return err
}

func (m *XrayManager) stopCoreProcess(cmd *exec.Cmd, done <-chan struct{}) error {
	stopTimeout := m.StopTimeout
	if stopTimeout == 0 {
		stopTimeout = defaultStopTimeout
	}
	return stopCoreProcessBounded(cmd, done, stopTimeout)
}

func inactiveXrayState() xrayState {
	return xrayState{
		Connection: "inactive",
		Proxy:      "inactive",
		TUN:        "disabled",
		Routes:     "not modified",
		DNS:        "not modified",
		Firewall:   "not modified",
	}
}

func lifecycleResponse(state xrayState) api.LifecycleResponse {
	return api.LifecycleResponse{
		Connection:        state.Connection,
		Mode:              state.Mode,
		ProfileID:         state.ProfileID,
		ProfileName:       state.ProfileName,
		Proxy:             state.Proxy,
		TUN:               state.TUN,
		Routes:            state.Routes,
		DNS:               state.DNS,
		Firewall:          state.Firewall,
		RuntimeConfigPath: state.RuntimeConfigPath,
		Warnings:          append([]string{}, state.Warnings...),
	}
}
