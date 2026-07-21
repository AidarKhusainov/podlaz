package daemon

import (
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
)

func TestTunPlanExecutorProductionCompositionUsesResolvedDNSVerifier(t *testing.T) {
	t.Setenv(e2eTunHookGateEnv, "")
	t.Setenv(e2eTunHookPhaseEnv, "")

	executor := NewXrayManager(t.TempDir()).tunPlanExecutor()
	composed, ok := executor.(netexecutor.DNSAwareTunExecutor)
	if !ok {
		t.Fatalf("unexpected production TUN executor type %T", executor)
	}
	if _, ok := composed.DNS.(netexecutor.ResolvedDNSExecutor); !ok {
		t.Fatalf("production TUN executor must use ResolvedDNSExecutor, got %T", composed.DNS)
	}
}
