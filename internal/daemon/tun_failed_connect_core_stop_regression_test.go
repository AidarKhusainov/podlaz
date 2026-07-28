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
	runner.finalizeFailureDiagnostics = func(context.Context, tunFailureDiagnosticSummary, string) {
		finalized = append(finalized, "unexpected")
	}
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
	if _, statErr := os.Stat(configPath); statErr != nil {
		t.Fatalf("generated config must remain while Xray absence is unproven: %v", statErr)
	}

	summaries, warnings := txstate.ScanTransactions(h.runtimeDir)
	if len(warnings) != 0 || len(summaries) != 1 {
		t.Fatalf("ownership metadata must remain after stop failure: summaries=%#v warnings=%#v", summaries, warnings)
	}
	if !summaries[0].RequiresCleanup || summaries[0].State == txstate.TransactionRolledBack {
		t.Fatalf("stop failure must remain cleanup-required: %#v", summaries[0])
	}
}

func TestStopCoreProcessEscalatesWhenXrayIgnoresSIGTERM(t *testing.T) {
	if os.Getenv("PODLAZ_TEST_IGNORE_XRAY_SIGTERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
		ready := os.Getenv("PODLAZ_TEST_XRAY_READY")
		if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil {
			os.Exit(97)
		}
		select {}
	}

	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestStopCoreProcessEscalatesWhenXrayIgnoresSIGTERM$")
	cmd.Env = append(os.Environ(),
		"PODLAZ_TEST_IGNORE_XRAY_SIGTERM=1",
		"PODLAZ_TEST_XRAY_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start SIGTERM-ignoring Xray fixture: %v", err)
	}
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

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspect Xray fixture readiness: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("SIGTERM-ignoring Xray fixture did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

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
