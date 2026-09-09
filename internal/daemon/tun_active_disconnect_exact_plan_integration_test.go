package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestXrayManagerActiveTunDisconnectReceivesExactPersistedFirewallPlan(t *testing.T) {
	runtimeDir := t.TempDir()
	configPath := filepath.Join(runtimeDir, generatedDirName, generatedXrayName)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	plan := transactionPlanForTest()
	plan.TunDevice.Action = "verify"
	plan.Firewall = terminalRecoveryFirewallPlan()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: fixedClock()}
	tx := txstate.NewTransaction("tun-exact-active-disconnect", "profile-example", planner.ModeTun, store.Now())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan = desiredPlanFromTunPlan(plan)
	tx.Rollback = rollbackMetadataFromTunPlan(plan)
	tx.AppliedSteps = appliedStepsFromRollbackMetadataForTest(tx.Rollback, store.Now())
	tx.Rollback.GeneratedConfigs = append(tx.Rollback.GeneratedConfigs, txstate.GeneratedConfigRollback{
		Path: configPath, Owner: txstate.TransactionOwner,
	})
	if _, err := store.Save(tx); err != nil {
		t.Fatal(err)
	}

	fakeXray := writeFakeXray(t, `#!/bin/sh
trap 'exit 0' TERM
while true; do sleep 3600 & wait $!; done
`)
	cmd := exec.Command(fakeXray)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
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
		case <-time.After(time.Second):
		}
	})

	executor := &exactPlanDisconnectExecutor{}
	manager := &XrayManager{RuntimeDir: runtimeDir, StopTimeout: 2 * time.Second, tunExecutor: executor}
	manager.mu.Lock()
	manager.cmd = cmd
	manager.done = done
	manager.state = xrayState{
		Connection:        "active",
		Mode:              planner.ModeTun,
		ProfileID:         "profile-example",
		ProfileName:       "Example profile",
		RuntimeConfigPath: configPath,
		TransactionID:     tx.ID,
	}
	manager.mu.Unlock()

	response, err := manager.Disconnect(context.Background())
	if err != nil {
		t.Fatalf("active TUN disconnect: %v", err)
	}
	if response.Connection != "inactive" {
		t.Fatalf("disconnect response=%#v, want inactive", response)
	}
	if executor.calls != 1 {
		t.Fatalf("rollback calls=%d, want 1", executor.calls)
	}
	if len(executor.plan.Firewall.Chains) == 0 || len(executor.plan.Firewall.Rules) == 0 {
		t.Fatalf("real XrayManager disconnect lost exact persisted firewall composition: %#v", executor.plan.Firewall)
	}
	if executor.plan.Firewall.Family != plan.Firewall.Family || executor.plan.Firewall.Table != plan.Firewall.Table {
		t.Fatalf("rollback firewall identity=%s %s, want %s %s", executor.plan.Firewall.Family, executor.plan.Firewall.Table, plan.Firewall.Family, plan.Firewall.Table)
	}
	if _, _, err := store.Load(tx.ID); err == nil {
		t.Fatal("successful active disconnect left transaction authority")
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("successful active disconnect left generated config: %v", err)
	}
}

type exactPlanDisconnectExecutor struct {
	calls int
	plan  planner.TunPlan
}

func (e *exactPlanDisconnectExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, nil
}

func (e *exactPlanDisconnectExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (e *exactPlanDisconnectExecutor) Rollback(_ context.Context, plan planner.TunPlan) error {
	e.calls++
	e.plan = plan
	return nil
}
