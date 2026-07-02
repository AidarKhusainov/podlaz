package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/doctor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/render"
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
	m.stopping = true
	m.mu.Unlock()

	if err := m.stopCoreProcess(cmd, done); err != nil {
		return api.LifecycleResponse{}, err
	}
	removeGeneratedConfig(configPath)
	if mode == planner.ModeTun {
		if transactionID == "" {
			return api.LifecycleResponse{}, errors.New("active TUN connection has no transaction id; run podlaz recover")
		}
		return m.disconnectTun(ctx, transactionID)
	}

	return lifecycleResponse(inactiveXrayState()), nil
}

func (m *XrayManager) Status(context.Context) api.StatusResponse {
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state
	if state.Connection == "" {
		state = inactiveXrayState()
	}
	transactions, transactionWarnings := transactionStatuses(m.runtimeDir())
	warnings := append([]string(nil), state.Warnings...)
	warnings = append(warnings, transactionWarnings...)
	return api.StatusResponse{
		Daemon:            "running",
		Service:           api.ServiceFromEnv(),
		Connection:        state.Connection,
		Mode:              state.Mode,
		ProfileID:         state.ProfileID,
		ProfileName:       state.ProfileName,
		RuntimeDirectory:  "present",
		RuntimeConfigPath: state.RuntimeConfigPath,
		Proxy:             state.Proxy,
		TUN:               state.TUN,
		Routes:            state.Routes,
		DNS:               state.DNS,
		Firewall:          state.Firewall,
		Transactions:      transactions,
		Warnings:          warnings,
	}
}

func (m *XrayManager) Doctor(ctx context.Context) api.DoctorResponse {
	report := doctor.RunWithOptions(ctx, doctor.Options{RuntimeDir: m.runtimeDir(), RuntimeDirOwnedByDaemon: true})
	report = doctor.WithSource(report, doctor.SourceDaemon)
	report = doctor.WithDaemonCheck(report, doctor.SeverityOK, "running")
	report.Checks = append(report.Checks, m.lifecycleDoctorChecks(ctx)...)
	return doctor.ToDaemon(report)
}

func (m *XrayManager) lifecycleDoctorChecks(ctx context.Context) []doctor.Check {
	m.mu.Lock()
	state := m.state
	coreRunning := m.cmd != nil
	m.mu.Unlock()
	if state.Connection == "" {
		state = inactiveXrayState()
	}

	coreSeverity := doctor.SeverityOK
	coreMessage := "inactive"
	switch {
	case state.Connection == "error (core exited)":
		coreSeverity = doctor.SeverityFail
		coreMessage = "core exited unexpectedly; inspect podlaz logs --core"
	case coreRunning:
		coreMessage = emptyAs(state.Proxy, "core process is running")
	case state.Connection == "active":
		coreSeverity = doctor.SeverityWarning
		coreMessage = "connection is active but no supervised Xray process is registered"
	}

	checks := []doctor.Check{{Name: "core", Severity: coreSeverity, Message: coreMessage}}
	if state.Mode != planner.ModeTun && state.TransactionID == "" {
		return checks
	}

	snapshot := m.collectTunSnapshot(ctx, tunSnapshotOptionsForState(state))
	checks = append(checks,
		tunDoctorCheck(state, snapshot),
		routeDoctorCheck(state, snapshot),
		dnsDoctorCheck(state, snapshot),
		firewallDoctorCheck(state, snapshot),
	)
	if state.TransactionID != "" {
		checks = append(checks, transactionDoctorCheck(m.runtimeDir(), state.TransactionID))
	}
	return checks
}

func (m *XrayManager) waitForExit(cmd *exec.Cmd, done chan struct{}, coreLogs []*coreLogWriter, runtimeConfigPath, profileID string) {
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
		logCoreStopped(pid, profileID)
		return
	}
	exitMessage := processExitMessage(err)
	m.state.Connection = "error (core exited)"
	m.state.Proxy = "inactive"
	m.state.Warnings = append(m.state.Warnings, exitMessage)
	logCoreExited(pid, profileID, exitMessage)
}

func (m *XrayManager) runtimeDir() string {
	if m.RuntimeDir != "" {
		return m.RuntimeDir
	}
	return api.RuntimeDirFromEnv()
}

func transactionStatuses(runtimeDir string) ([]api.TransactionStatus, []string) {
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
	return statuses, warnings
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
	stdoutLog := newCoreLogWriter(p.ID, "stdout")
	stderrLog := newCoreLogWriter(p.ID, "stderr")
	cmd.Stdout = stdoutLog
	cmd.Stderr = stderrLog
	configureCoreCommandCredential(cmd, identity)
	if err := cmd.Start(); err != nil {
		removeGeneratedConfig(runtimeConfigPath)
		logCoreStartFailed(p.ID, err)
		return nil, nil, fmt.Errorf("start Xray: %w", err)
	}

	pid := cmd.Process.Pid
	stdoutLog.setPID(pid)
	stderrLog.setPID(pid)
	logCoreStarted(pid, p.ID)

	done := make(chan struct{})
	m.cmd = cmd
	m.done = done
	m.stopping = false
	go m.waitForExit(cmd, done, []*coreLogWriter{stdoutLog, stderrLog}, runtimeConfigPath, p.ID)
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
	if cmd == nil {
		return nil
	}
	if cmd.Process != nil {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("stop Xray gracefully: %w", err)
		}
	}
	if done == nil {
		return nil
	}

	stopTimeout := m.StopTimeout
	if stopTimeout == 0 {
		stopTimeout = defaultStopTimeout
	}
	select {
	case <-done:
	case <-time.After(stopTimeout):
		if cmd.Process != nil {
			if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return fmt.Errorf("force stop Xray: %w", err)
			}
		}
		<-done
	}
	return nil
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
		Warnings:          append([]string(nil), state.Warnings...),
	}
}

func profileFromSnapshot(p api.ProfileSnapshot) profile.Profile {
	return profile.Profile{
		ID:               p.ID,
		Name:             p.Name,
		Source:           profile.SourceType(p.Source),
		Engine:           profile.Engine(p.Engine),
		Server:           p.Server,
		Port:             p.Port,
		Protocol:         p.Protocol,
		UserIdentity:     p.UserIdentity,
		Transport:        p.Transport,
		Security:         p.Security,
		Encryption:       p.Encryption,
		Flow:             p.Flow,
		ServerName:       p.ServerName,
		ALPN:             p.ALPN,
		Fingerprint:      p.Fingerprint,
		Path:             p.Path,
		HostHeader:       p.HostHeader,
		ServiceName:      p.ServiceName,
		RealityPublicKey: p.RealityPublicKey,
		RealityShortID:   p.RealityShortID,
		RealitySpiderX:   p.RealitySpiderX,
	}
}

func proxyListenersLine(listeners []planner.Listener) string {
	parts := make([]string, 0, len(listeners))
	for _, listener := range listeners {
		parts = append(parts, fmt.Sprintf("%s (%s)", listener.Endpoint(), strings.ToUpper(listener.Protocol)))
	}
	if len(parts) == 0 {
		return "inactive"
	}
	return "listening on " + strings.Join(parts, ", ")
}

func processExitMessage(err error) string {
	if err == nil {
		return "Xray process exited unexpectedly"
	}
	return "Xray process exited unexpectedly: " + err.Error()
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

type coreLogWriter struct {
	mu         sync.Mutex
	pid        int
	pidKnown   bool
	profileID  string
	streamName string
	pending    []byte
}

func newCoreLogWriter(profileID, streamName string) *coreLogWriter {
	return &coreLogWriter{profileID: profileID, streamName: streamName}
}

func (w *coreLogWriter) setPID(pid int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pid = pid
	w.pidKnown = true
	w.flushCompleteLinesLocked()
}

func (w *coreLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written := len(p)
	w.pending = append(w.pending, p...)
	if w.pidKnown {
		w.flushCompleteLinesLocked()
	}
	return written, nil
}

func (w *coreLogWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushCompleteLinesLocked()
	if len(w.pending) == 0 {
		return
	}
	w.logLineLocked(w.pending)
	w.pending = w.pending[:0]
}

func (w *coreLogWriter) flushCompleteLinesLocked() {
	for {
		idx := bytes.IndexByte(w.pending, '\n')
		if idx < 0 {
			return
		}
		line := w.pending[:idx]
		w.pending = w.pending[idx+1:]
		w.logLineLocked(line)
	}
}

func (w *coreLogWriter) logLineLocked(line []byte) {
	message := strings.TrimSpace(string(line))
	if message == "" {
		return
	}
	pidPart := "unknown"
	if w.pidKnown {
		pidPart = fmt.Sprintf("%d", w.pid)
	}
	log.Printf("core profile=%s pid=%s stream=%s line=%s", render.Redact(w.profileID), pidPart, w.streamName, render.Redact(message))
}
