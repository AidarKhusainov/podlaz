package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestE2ETerminalFirewallRollbackBlocksOnceBeforeDelegateMutation(t *testing.T) {
	t.Setenv(e2eTerminalFirewallRollbackOnceEnv, "true")
	t.Setenv(e2eTunHookDirEnv, t.TempDir())
	delegate := &terminalRollbackFirewallStub{}
	executor := maybeWrapE2ETerminalFirewallRollback(netexecutor.DNSAwareTunExecutor{Firewall: delegate})

	plan := terminalRecoveryFirewallPlan()
	if err := executor.Firewall.Rollback(context.Background(), plan); err == nil {
		t.Fatal("first terminal firewall rollback must be injected as a blocker")
	}
	if delegate.rollbackCalls != 0 {
		t.Fatalf("injected blocker reached firewall mutation delegate %d time(s)", delegate.rollbackCalls)
	}
	marker := filepath.Join(e2eTunHookDir(), e2eTerminalFirewallRollbackMarkerName)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("first injected rollback did not persist one-shot marker: %v", err)
	}

	if err := executor.Firewall.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("second terminal firewall rollback must delegate normally: %v", err)
	}
	if delegate.rollbackCalls != 1 {
		t.Fatalf("second rollback delegate calls=%d, want 1", delegate.rollbackCalls)
	}
}

func TestE2ETerminalFirewallRollbackRejectsIncompletePlanWithoutConsumingInjection(t *testing.T) {
	t.Setenv(e2eTerminalFirewallRollbackOnceEnv, "true")
	t.Setenv(e2eTunHookDirEnv, t.TempDir())
	delegate := &terminalRollbackFirewallStub{}
	executor := maybeWrapE2ETerminalFirewallRollback(netexecutor.DNSAwareTunExecutor{Firewall: delegate})

	err := executor.Firewall.Rollback(context.Background(), planner.TunFirewallPlan{Family: "inet", Table: "podlaz"})
	if err == nil {
		t.Fatal("incomplete exact firewall plan must fail closed")
	}
	if delegate.rollbackCalls != 0 {
		t.Fatalf("incomplete plan reached firewall delegate %d time(s)", delegate.rollbackCalls)
	}
	marker := filepath.Join(e2eTunHookDir(), e2eTerminalFirewallRollbackMarkerName)
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("incomplete plan consumed one-shot terminal rollback injection: %v", statErr)
	}
}

type terminalRollbackFirewallStub struct {
	rollbackCalls int
}

func (s *terminalRollbackFirewallStub) Apply(context.Context, planner.TunFirewallPlan) (netexecutor.Step, error) {
	return netexecutor.Step{}, nil
}

func (s *terminalRollbackFirewallStub) Verify(context.Context, planner.TunFirewallPlan) error {
	return nil
}

func (s *terminalRollbackFirewallStub) Rollback(context.Context, planner.TunFirewallPlan) error {
	s.rollbackCalls++
	return nil
}
