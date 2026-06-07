package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
	"github.com/AidarKhusainov/tunwarden/internal/profile"
	"github.com/AidarKhusainov/tunwarden/internal/render"
)

const (
	defaultStopTimeout = 5 * time.Second
	generatedDirName   = "generated"
	generatedXrayName  = "xray.json"
)

// XrayManager owns the daemon-side proxy-only Xray process lifecycle.
type XrayManager struct {
	RuntimeDir  string
	XrayPath    string
	StopTimeout time.Duration

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
	Warnings          []string
}

func NewXrayManager(runtimeDir string) *XrayManager {
	return &XrayManager{RuntimeDir: runtimeDir}
}

func (m *XrayManager) Connect(ctx context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
	_ = ctx
	if err := api.ValidateConnectRequest(req); err != nil {
		return api.LifecycleResponse{}, err
	}
	if strings.TrimSpace(req.Mode) != planner.ModeProxyOnly {
		return api.LifecycleResponse{}, fmt.Errorf("unsupported connect mode %q", req.Mode)
	}
	p := profileFromSnapshot(req.Profile)
	if err := profile.Validate(p); err != nil {
		return api.LifecycleResponse{}, err
	}
	if os.Geteuid() == 0 {
		return api.LifecycleResponse{}, errors.New("refusing to start proxy-only Xray as root; run tunwardend as the packaged unprivileged tunwarden user or use a user-owned development runtime directory")
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
	if m.cmd != nil {
		m.mu.Unlock()
		return api.LifecycleResponse{}, errors.New("connection already active; run tunwarden disconnect before connecting another profile")
	}

	if err := writeRuntimeConfig(runtimeConfigPath, proxyPlan.XrayConfig); err != nil {
		m.mu.Unlock()
		return api.LifecycleResponse{}, err
	}

	cmd := exec.Command(xrayPath, "run", "-config", runtimeConfigPath)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		m.mu.Unlock()
		removeGeneratedConfig(runtimeConfigPath)
		return api.LifecycleResponse{}, fmt.Errorf("prepare Xray stdout: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		m.mu.Unlock()
		removeGeneratedConfig(runtimeConfigPath)
		return api.LifecycleResponse{}, fmt.Errorf("prepare Xray stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		removeGeneratedConfig(runtimeConfigPath)
		logCoreStartFailed(p.ID, err)
		return api.LifecycleResponse{}, fmt.Errorf("start Xray: %w", err)
	}

	pid := cmd.Process.Pid
	outputDone := forwardCoreOutput(pid, p.ID, stdoutPipe, stderrPipe)
	logCoreStarted(pid, p.ID)

	done := make(chan struct{})
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

	m.cmd = cmd
	m.done = done
	m.stopping = false
	m.state = active
	m.mu.Unlock()

	go m.waitForExit(cmd, done, outputDone, runtimeConfigPath, p.ID)
	return lifecycleResponse(active), nil
}

func (m *XrayManager) Disconnect(ctx context.Context) (api.LifecycleResponse, error) {
	_ = ctx
	m.mu.Lock()
	cmd := m.cmd
	done := m.done
	configPath := m.state.RuntimeConfigPath
	if cmd == nil {
		m.state = inactiveXrayState()
		m.mu.Unlock()
		removeGeneratedConfig(configPath)
		return lifecycleResponse(inactiveXrayState()), nil
	}
	m.stopping = true
	m.mu.Unlock()

	if cmd.Process != nil {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return api.LifecycleResponse{}, fmt.Errorf("stop Xray gracefully: %w", err)
		}
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
				return api.LifecycleResponse{}, fmt.Errorf("force stop Xray: %w", err)
			}
		}
		<-done
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
	return api.StatusResponse{
		Daemon:            "running",
		Service:           api.ServiceFromEnv(),
		Connection:        state.Connection,
		Mode:              state.Mode,
		RuntimeDirectory:  "present",
		RuntimeConfigPath: state.RuntimeConfigPath,
		Proxy:             state.Proxy,
		TUN:               state.TUN,
		Routes:            state.Routes,
		DNS:               state.DNS,
		Firewall:          state.Firewall,
		Warnings:          append([]string(nil), state.Warnings...),
	}
}

func (m *XrayManager) waitForExit(cmd *exec.Cmd, done chan struct{}, outputDone <-chan struct{}, runtimeConfigPath, profileID string) {
	err := cmd.Wait()
	if outputDone != nil {
		<-outputDone
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

func forwardCoreOutput(pid int, profileID string, stdout, stderr io.Reader) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			scanCoreOutput(pid, profileID, "stdout", stdout)
		}()
		go func() {
			defer wg.Done()
			scanCoreOutput(pid, profileID, "stderr", stderr)
		}()
		wg.Wait()
		close(done)
	}()
	return done
}

func scanCoreOutput(pid int, profileID, streamName string, src io.Reader) {
	reader := bufio.NewReader(src)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			log.Printf("tunwardend: core xray %s pid=%d profile=%s: %s", streamName, pid, render.Redact(profileID), render.Redact(line))
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return
		}
		log.Printf("tunwardend: core xray log read failed stream=%s pid=%d profile=%s error=%s", streamName, pid, render.Redact(profileID), render.Redact(err.Error()))
		return
	}
}

func logCoreStarted(pid int, profileID string) {
	log.Printf("tunwardend: core xray started pid=%d profile=%s", pid, render.Redact(profileID))
}

func logCoreStartFailed(profileID string, err error) {
	log.Printf("tunwardend: core xray start failed profile=%s error=%s", render.Redact(profileID), render.Redact(err.Error()))
}

func logCoreStopped(pid int, profileID string) {
	log.Printf("tunwardend: core xray stopped pid=%d profile=%s", pid, render.Redact(profileID))
}

func logCoreExited(pid int, profileID, message string) {
	log.Printf("tunwardend: core xray exited pid=%d profile=%s error=%s", pid, render.Redact(profileID), render.Redact(message))
}

func writeRuntimeConfig(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create generated runtime config directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure generated runtime config directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".xray-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary generated Xray config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary generated Xray config: %w", err)
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary generated Xray config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary generated Xray config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary generated Xray config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace generated Xray config atomically: %w", err)
	}
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync generated Xray config directory: %w", err)
	}
	return nil
}

func removeGeneratedConfig(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
	_ = os.Remove(filepath.Dir(path))
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
