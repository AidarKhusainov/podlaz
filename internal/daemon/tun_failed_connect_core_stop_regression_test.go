package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

const (
	sigtermIgnoringFixtureEnv   = "PODLAZ_TEST_IGNORE_XRAY_SIGTERM"
	sigtermIgnoringReadyPathEnv = "PODLAZ_TEST_XRAY_READY"
)

func TestFailedConnectPreservesOwnershipWhenSupervisedCoreStopFails(t *testing.T) {
	h := newFullTunnelRunnerHarness(t)
	h.verifyConnectivityErr = errRunnerConnectivityFailed

	configPath := filepath.Join(h.runtimeDir, generatedDirName, generatedXrayName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("create generated config directory: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatalf("write generated config: %v", err)
	}

	forceStopErr := errors.New("force stop Xray: injected failure")
	runner := h.runner()
	runner.stopCore = func(fullTunnelCoreHandle) error {
		h.coreStopped++
		return forceStopErr
	}
	var finalized []string
	runner.finalizeFailureDiagnostics = func(_ context.Context, _ tunFailureDiagnosticSummary, status string) {
		finalized = append(finalized, status)
	}

	_, err := runner.run(context.Background())
	if err == nil {
		t.Fatal("expected failed-connect convergence error")
	}
	if !errors.Is(err, errRunnerConnectivityFailed) || !errors.Is(err, forceStopErr) {
		t.Fatalf("expected connectivity and supervised stop errors, got %v", err)
	}
	phase, _, rollbackStatus := tunFailureLogFields(err)
	if phase != "connectivity-verify" || rollbackStatus != "failed" {
		t.Fatalf("unexpected failed-connect fields: phase=%q rollback_status=%q err=%v", phase, rollbackStatus, err)
	}
	if strings.Join(finalized, ",") != "failed" {
		t.Fatalf("diagnostics finalized before proven process quiescence: %#v", finalized)
	}
	if strings.Contains(err.Error(), "Rollback completed") || strings.Contains(err.Error(), "rolled back applied") {
		t.Fatalf("failed process convergence was reported as completed: %v", err)
	}
	assertFailedConnectOwnershipPreserved(t, h.runtimeDir, configPath)
}

func TestFailedConnectBoundsPostKillQuiescenceAndPreservesOwnership(t *testing.T) {
	runSIGTERMIgnoringFixtureIfRequested()

	h := newFullTunnelRunnerHarness(t)
	h.verifyConnectivityErr = errRunnerConnectivityFailed
	configPath := filepath.Join(h.runtimeDir, generatedDirName, generatedXrayName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("create generated config directory: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatalf("write generated config: %v", err)
	}

	cmd, _ := startSIGTERMIgnoringFixture(t, "TestFailedConnectBoundsPostKillQuiescenceAndPreservesOwnership")
	neverDone := make(chan struct{})
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		waited := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(waited)
		}()
		select {
		case <-waited:
		case <-time.After(2 * time.Second):
		}
	})

	manager := &XrayManager{StopTimeout: 40 * time.Millisecond}
	runner := h.runner()
	runner.startCore = func(context.Context) (fullTunnelCoreHandle, error) {
		return fullTunnelCoreHandle{cmd: cmd, done: neverDone, pid: cmd.Process.Pid}, nil
	}
	runner.stopCore = func(core fullTunnelCoreHandle) error {
		h.coreStopped++
		return manager.stopStartedCoreForTransaction(core.cmd, core.done)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := runner.run(context.Background())
		errCh <- err
	}()

	var err error
	select {
	case err = <-errCh:
	case <-time.After(750 * time.Millisecond):
		t.Fatal("failed-connect rollback remained blocked after successful SIGKILL with an unclosed completion signal")
	}
	if err == nil || !strings.Contains(err.Error(), "quiescence") {
		t.Fatalf("expected bounded post-KILL quiescence failure, got %v", err)
	}
	phase, _, rollbackStatus := tunFailureLogFields(err)
	if phase != "connectivity-verify" || rollbackStatus != "failed" {
		t.Fatalf("unexpected failed-connect fields: phase=%q rollback_status=%q err=%v", phase, rollbackStatus, err)
	}
	assertFailedConnectOwnershipPreserved(t, h.runtimeDir, configPath)
}

func TestStopCoreProcessEscalatesWhenXrayIgnoresSIGTERM(t *testing.T) {
	runSIGTERMIgnoringFixtureIfRequested()

	cmd, _ := startSIGTERMIgnoringFixture(t, "TestStopCoreProcessEscalatesWhenXrayIgnoresSIGTERM")
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})

	manager := &XrayManager{StopTimeout: 50 * time.Millisecond}
	started := time.Now()
	if err := manager.stopCoreProcess(cmd, done); err != nil {
		t.Fatalf("bounded supervised stop: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("bounded supervised stop took too long: %s", elapsed)
	}
	select {
	case <-done:
	default:
		t.Fatal("force-stopped Xray process was not reaped")
	}
}

func assertFailedConnectOwnershipPreserved(t *testing.T, runtimeDir, configPath string) {
	t.Helper()
	if _, statErr := os.Stat(configPath); statErr != nil {
		t.Fatalf("generated config must remain while Xray absence is unproven: %v", statErr)
	}

	summaries, warnings := txstate.ScanTransactions(runtimeDir)
	if len(warnings) != 0 || len(summaries) != 1 {
		t.Fatalf("ownership metadata must remain after stop failure: summaries=%#v warnings=%#v", summaries, warnings)
	}
	if !summaries[0].RequiresCleanup || summaries[0].State == txstate.TransactionRolledBack {
		t.Fatalf("stop failure must remain cleanup-required: %#v", summaries[0])
	}
}

func runSIGTERMIgnoringFixtureIfRequested() {
	if os.Getenv(sigtermIgnoringFixtureEnv) != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	ready := os.Getenv(sigtermIgnoringReadyPathEnv)
	if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil {
		os.Exit(97)
	}
	select {}
}

func startSIGTERMIgnoringFixture(t *testing.T, testName string) (*exec.Cmd, string) {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	cmd.Env = append(os.Environ(),
		sigtermIgnoringFixtureEnv+"=1",
		sigtermIgnoringReadyPathEnv+"="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start SIGTERM-ignoring Xray fixture: %v", err)
	}
	waitForSIGTERMIgnoringFixture(t, ready)
	return cmd, ready
}

func waitForSIGTERMIgnoringFixture(t *testing.T, ready string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspect Xray fixture readiness: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("SIGTERM-ignoring Xray fixture did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
