package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	e2eTerminalFirewallRollbackOnceEnv    = "PODLAZ_E2E_TERMINAL_FIREWALL_ROLLBACK_ONCE"
	e2eTerminalFirewallRollbackMarkerName = "terminal-firewall-rollback.injected"
)

// maybeWrapE2ETerminalFirewallRollback is a dedicated real-host test seam for
// terminal teardown. The first exact firewall rollback is rejected before any
// nftables mutation; a retry in the same daemon delegates normally. Production
// behavior is unchanged unless the explicit E2E gate is enabled.
func maybeWrapE2ETerminalFirewallRollback(executor netexecutor.DNSAwareTunExecutor) netexecutor.DNSAwareTunExecutor {
	if !e2eBoolEnv(e2eTerminalFirewallRollbackOnceEnv) {
		return executor
	}
	executor.Firewall = e2eTerminalFirewallRollbackExecutor{delegate: executor.Firewall}
	return executor
}

type e2eTerminalFirewallRollbackExecutor struct {
	delegate netexecutor.FirewallExecutor
}

func (e e2eTerminalFirewallRollbackExecutor) Apply(ctx context.Context, plan planner.TunFirewallPlan) (netexecutor.Step, error) {
	if e.delegate == nil {
		return netexecutor.Step{}, errors.New("missing firewall executor")
	}
	return e.delegate.Apply(ctx, plan)
}

func (e e2eTerminalFirewallRollbackExecutor) Verify(ctx context.Context, plan planner.TunFirewallPlan) error {
	if e.delegate == nil {
		return errors.New("missing firewall executor")
	}
	return e.delegate.Verify(ctx, plan)
}

func (e e2eTerminalFirewallRollbackExecutor) Rollback(ctx context.Context, plan planner.TunFirewallPlan) error {
	if e.delegate == nil {
		return errors.New("missing firewall executor")
	}
	marker := filepath.Join(e2eTunHookDir(), e2eTerminalFirewallRollbackMarkerName)
	if _, err := os.Stat(marker); err == nil {
		return e.delegate.Rollback(ctx, plan)
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect E2E terminal firewall rollback marker: " + err.Error())
	}
	if strings.TrimSpace(plan.Family) == "" || strings.TrimSpace(plan.Table) == "" || len(plan.Chains) == 0 || len(plan.Rules) == 0 {
		return errors.New("E2E terminal firewall rollback requires the complete exact firewall plan")
	}
	if err := os.MkdirAll(e2eTunHookDir(), 0o700); err != nil {
		return errors.New("create E2E terminal firewall rollback marker directory: " + err.Error())
	}
	if err := os.WriteFile(marker, []byte("blocked-before-nftables-mutation\n"), 0o600); err != nil {
		return errors.New("write E2E terminal firewall rollback marker: " + err.Error())
	}
	return errors.New("E2E hook: terminal firewall rollback blocked before nftables mutation")
}

func e2eBoolEnv(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	return value == "1" || strings.EqualFold(value, "true")
}
