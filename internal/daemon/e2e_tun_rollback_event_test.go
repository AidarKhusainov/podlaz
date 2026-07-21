package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestMaybeRecordE2EDNSRollbackRecordsActualDelegateCall(t *testing.T) {
	hookDir := t.TempDir()
	t.Setenv(e2eTunHookGateEnv, "true")
	t.Setenv(e2eTunHookPhaseEnv, e2eTunHookDNSApplyPhase)
	t.Setenv(e2eTunHookDirEnv, hookDir)

	dns := &rollbackCountingDNSExecutor{}
	executor := netexecutor.DNSAwareTunExecutor{DNS: dns}
	wrapped := maybeRecordE2EDNSRollback(executor)
	dnsAware, ok := wrapped.(netexecutor.DNSAwareTunExecutor)
	if !ok {
		t.Fatalf("expected DNS-aware executor, got %T", wrapped)
	}
	if err := dnsAware.DNS.Rollback(context.Background(), planner.TunDNSPlan{TargetLink: "podlaz0"}); err != nil {
		t.Fatalf("rollback wrapped DNS executor: %v", err)
	}
	if dns.rollbackCalls != 1 {
		t.Fatalf("delegate rollback calls = %d, want 1", dns.rollbackCalls)
	}
	events, err := os.ReadFile(filepath.Join(hookDir, e2eTunHookEventsFile))
	if err != nil {
		t.Fatalf("read E2E event log: %v", err)
	}
	if !strings.Contains(string(events), "dns-rollback-started\n") {
		t.Fatalf("actual DNS rollback event was not recorded: %q", events)
	}
}

type rollbackCountingDNSExecutor struct {
	rollbackCalls int
}

func (*rollbackCountingDNSExecutor) Apply(context.Context, planner.TunDNSPlan) (netexecutor.Step, error) {
	return netexecutor.Step{}, nil
}

func (*rollbackCountingDNSExecutor) Verify(context.Context, planner.TunDNSPlan) error {
	return nil
}

func (e *rollbackCountingDNSExecutor) Rollback(context.Context, planner.TunDNSPlan) error {
	e.rollbackCalls++
	return nil
}
