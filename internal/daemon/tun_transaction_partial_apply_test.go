package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

func TestDirectTunTransactionRollsBackPartialDNSApplyAtTransactionBoundary(t *testing.T) {
	runtimeDir := t.TempDir()
	plan := transactionPlanForTest()
	plan.DNS = planner.TunDNSPlan{
		Backend:    planner.DNSBackendSystemdResolved,
		TargetLink: "podlaz0",
		Servers:    []string{planner.DefaultTunDNSServer},
		Action:     planner.DNSActionConfigure,
	}
	executor := &partialDNSMutationExecutor{}

	result, err := beginTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, plan, fixedClock())
	if err != nil {
		t.Fatalf("begin TUN transaction: %v", err)
	}
	err = applyVerifyTunTransaction(context.Background(), result, executor)
	if err == nil || !strings.Contains(err.Error(), "rolled back applied") {
		t.Fatalf("expected direct helper to report completed rollback, got %v", err)
	}
	if strings.Join(executor.calls, ",") != "apply,rollback" {
		t.Fatalf("rollback must occur once at the transaction boundary, calls=%#v", executor.calls)
	}
	if executor.rollbackPlan.DNS.TargetLink != "podlaz0" || executor.rollbackPlan.DNS.Action != planner.DNSActionConfigure {
		t.Fatalf("partial DNS ownership was not propagated into rollback plan: %#v", executor.rollbackPlan.DNS)
	}
	summaries, warnings := transactionStatuses(runtimeDir)
	if len(warnings) != 0 || len(summaries) != 0 {
		t.Fatalf("successful direct rollback must remove failed transaction state: summaries=%#v warnings=%#v", summaries, warnings)
	}
}

type partialDNSMutationExecutor struct {
	calls        []string
	rollbackPlan planner.TunPlan
}

func (e *partialDNSMutationExecutor) Apply(_ context.Context, plan planner.TunPlan) ([]netexecutor.Step, error) {
	e.calls = append(e.calls, "apply")
	return []netexecutor.Step{
		{Kind: "tun-device", Target: plan.TunDevice.Name, Owner: netexecutor.OwnerTunDevice},
		{Kind: "dns", Target: plan.DNS.TargetLink, Owner: netexecutor.OwnerDNS},
	}, errors.New("DNS apply failed after mutation")
}

func (e *partialDNSMutationExecutor) Verify(context.Context, planner.TunPlan) error {
	return errors.New("verify must not run after apply failure")
}

func (e *partialDNSMutationExecutor) Rollback(_ context.Context, plan planner.TunPlan) error {
	e.calls = append(e.calls, "rollback")
	e.rollbackPlan = plan
	return nil
}
