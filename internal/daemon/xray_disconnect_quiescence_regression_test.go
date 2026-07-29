package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestXrayManagerDisconnectFixture(t *testing.T) {
	if os.Getenv("PODLAZ_TEST_STUCK_XRAY_COMPLETION") != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	ready := os.Getenv("PODLAZ_TEST_STUCK_XRAY_READY")
	if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil {
		os.Exit(97)
	}
	select {}
}

func TestProxyDisconnectReturnsBoundedQuiescenceErrorAfterSuccessfulKill(t *testing.T) {
	runtimeDir := t.TempDir()
	configPath := writeDisconnectRegressionConfig(t, runtimeDir)
	cmd, done, releaseDone := startCoreWithStuckCompletion(t)

	manager := &XrayManager{RuntimeDir: runtimeDir, StopTimeout: 50 * time.Millisecond}
	manager.mu.Lock()
	manager.cmd = cmd
	manager.done = done
	manager.state = xrayState{
		Connection:        "active",
		Mode:              planner.ModeProxyOnly,
		RuntimeConfigPath: configPath,
	}
	manager.mu.Unlock()

	err, returnedWithinBound := runDisconnectWithBound(manager, releaseDone)
	if !returnedWithinBound {
		t.Fatal("proxy disconnect remained blocked after the bounded post-KILL interval")
	}
	if err == nil || !strings.Contains(err.Error(), "quiescence") {
		t.Fatalf("expected bounded Xray quiescence error, got %v", err)
	}
	if _, statErr := os.Stat(configPath); statErr != nil {
		t.Fatalf("proxy config must remain when process quiescence is unproven: %v", statErr)
	}
}

func TestActiveTunDisconnectReturnsBoundedQuiescenceErrorAndPreservesRecoveryState(t *testing.T) {
	runtimeDir := t.TempDir()
	configPath := writeDisconnectRegressionConfig(t, runtimeDir)
	plan := transactionPlanForTest()
	plan.TunDevice.Action = "verify"
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: fixedClock()}
	tx := txstate.NewTransaction("tun-stuck-disconnect", "test-profile", planner.ModeTun, store.Now())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan = desiredPlanFromTunPlan(plan)
	tx.Rollback = rollbackMetadataFromTunPlan(plan)
	tx.Rollback.GeneratedConfigs = append(tx.Rollback.GeneratedConfigs, txstate.GeneratedConfigRollback{
		Path:  configPath,
		Owner: txstate.TransactionOwner,
	})
	if _, err := store.Save(tx); err != nil {
		t.Fatalf("save active TUN transaction: %v", err)
	}

	cmd, done, releaseDone := startCoreWithStuckCompletion(t)
	manager := &XrayManager{
		RuntimeDir:  runtimeDir,
		StopTimeout: 50 * time.Millisecond,
		tunExecutor: boundedDisconnectExecutor{},
	}
	manager.mu.Lock()
	manager.cmd = cmd
	manager.done = done
	manager.state = xrayState{
		Connection:        "active",
		Mode:              planner.ModeTun,
		RuntimeConfigPath: configPath,
		TransactionID:     tx.ID,
	}
	manager.mu.Unlock()

	err, returnedWithinBound := runDisconnectWithBound(manager, releaseDone)
	if !returnedWithinBound {
		t.Fatal("active TUN disconnect remained blocked after the bounded post-KILL interval")
	}
	if err == nil || !strings.Contains(err.Error(), "quiescence") {
		t.Fatalf("expected bounded Xray quiescence error, got %v", err)
	}
	if _, statErr := os.Stat(configPath); statErr != nil {
		t.Fatalf("TUN config must remain when process quiescence is unproven: %v", statErr)
	}
	preserved, _, loadErr := store.Load(tx.ID)
	if loadErr != nil {
		t.Fatalf("TUN transaction must remain recoverable after stop failure: %v", loadErr)
	}
	if !preserved.RequiresCleanup() || preserved.State == txstate.TransactionRolledBack {
		t.Fatalf("TUN stop failure must preserve cleanup-required transaction state: %#v", preserved)
	}
}

type boundedDisconnectExecutor struct{}

func (boundedDisconnectExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, nil
}

func (boundedDisconnectExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (boundedDisconnectExecutor) Rollback(context.Context, planner.TunPlan) error { return nil }

func writeDisconnectRegressionConfig(t *testing.T, runtimeDir string) string {
	t.Helper()
	configPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("create generated config directory: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatalf("write generated config: %v", err)
	}
	return configPath
}

func startCoreWithStuckCompletion(t *testing.T) (*exec.Cmd, chan struct{}, func()) {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestXrayManagerDisconnectFixture$")
	cmd.Env = append(os.Environ(),
		"PODLAZ_TEST_STUCK_XRAY_COMPLETION=1",
		"PODLAZ_TEST_STUCK_XRAY_READY="+ready,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stuck-completion Xray fixture: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("inspect Xray fixture readiness: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("stuck-completion Xray fixture did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	done := make(chan struct{})
	var releaseOnce sync.Once
	releaseDone := func() { releaseOnce.Do(func() { close(done) }) }
	t.Cleanup(func() {
		releaseDone()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	return cmd, done, releaseDone
}

func runDisconnectWithBound(manager *XrayManager, releaseDone func()) (error, bool) {
	result := make(chan error, 1)
	go func() {
		_, err := manager.Disconnect(context.Background())
		result <- err
	}()

	select {
	case err := <-result:
		return err, true
	case <-time.After(500 * time.Millisecond):
		releaseDone()
		return <-result, false
	}
}
