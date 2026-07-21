package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const productionTunLinkForTest = `7: podlaz0: <POINTOPOINT,NOARP,UP,LOWER_UP> mtu 1500 qdisc fq_codel state UNKNOWN mode DEFAULT group default qlen 500
    link/none
    tun type tun pi off vnet_hdr on persist off`

const productionResolvedInactiveScopeForTest = `Global
       Protocols: +LLMNR -mDNS

Link 7 (podlaz0)
    Current Scopes: none
         Protocols: +DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
Current DNS Server: 1.1.1.1
       DNS Servers: 1.1.1.1 9.9.9.9
        DNS Domain: ~.`

func TestProductionTunExecutorTransactionFlowAcceptsInactiveScopeAndConverges(t *testing.T) {
	t.Setenv(e2eTunHookGateEnv, "")
	t.Setenv(e2eTunHookPhaseEnv, "")

	runtimeDir := t.TempDir()
	runner := &productionTunCommandRunner{resolvedStatus: productionResolvedInactiveScopeForTest}
	executor := newProductionTunPlanExecutor(runner)
	plan := productionDNSPlanForTest()
	p := profile.Profile{ID: "example-profile", Name: "Example Profile"}

	first := beginAndApplyProductionTunTransaction(t, runtimeDir, p, plan, executor)
	if err := rollbackVerifiedTunTransaction(context.Background(), runtimeDir, first.TransactionID, plan, executor); err != nil {
		t.Fatalf("rollback verified production transaction: %v", err)
	}
	assertNoProductionTunTransactionBlocker(t, runtimeDir)

	runner.resolvedStatus = strings.Replace(productionResolvedInactiveScopeForTest, " 9.9.9.9", "", 1)
	failed, err := beginTunTransaction(context.Background(), runtimeDir, p, plan, fixedClock())
	if err != nil {
		t.Fatalf("begin mismatched production transaction: %v", err)
	}
	err = applyVerifyTunTransaction(context.Background(), failed, executor)
	if err == nil || !strings.Contains(err.Error(), "DNS server 9.9.9.9 not found") {
		t.Fatalf("production composition must fail closed on DNS mismatch, got %v", err)
	}
	assertNoProductionTunTransactionBlocker(t, runtimeDir)

	runner.resolvedStatus = productionResolvedInactiveScopeForTest
	retry := beginAndApplyProductionTunTransaction(t, runtimeDir, p, plan, executor)
	if err := rollbackVerifiedTunTransaction(context.Background(), runtimeDir, retry.TransactionID, plan, executor); err != nil {
		t.Fatalf("rollback immediate retry transaction: %v", err)
	}
	assertNoProductionTunTransactionBlocker(t, runtimeDir)

	if runner.count("resolvectl status --no-pager") < 3 {
		t.Fatalf("production transaction flow did not execute resolved verification: %#v", runner.commands)
	}
	if runner.count("resolvectl revert podlaz0") < 5 {
		t.Fatalf("production transaction flow did not execute apply/rollback convergence: %#v", runner.commands)
	}
}

func beginAndApplyProductionTunTransaction(t *testing.T, runtimeDir string, p profile.Profile, plan planner.TunPlan, executor tunPlanExecutor) tunTransactionResult {
	t.Helper()
	result, err := beginTunTransaction(context.Background(), runtimeDir, p, plan, fixedClock())
	if err != nil {
		t.Fatalf("begin production TUN transaction: %v", err)
	}
	if err := applyVerifyTunTransaction(context.Background(), result, executor); err != nil {
		t.Fatalf("apply and verify production TUN transaction: %v", err)
	}
	return result
}

func assertNoProductionTunTransactionBlocker(t *testing.T, runtimeDir string) {
	t.Helper()
	summaries, warnings := transactionStatuses(runtimeDir)
	if len(summaries) != 0 || len(warnings) != 0 {
		t.Fatalf("rollback left a stale transaction/startup-scan blocker: summaries=%#v warnings=%#v", summaries, warnings)
	}
}

func productionDNSPlanForTest() planner.TunPlan {
	return planner.TunPlan{
		Mode:        planner.ModeTun,
		ProfileID:   "example-profile",
		ProfileName: "Example Profile",
		TunDevice: planner.TunDevicePlan{
			Name:   "podlaz0",
			MTU:    1500,
			Action: "verify",
		},
		DNS: planner.TunDNSPlan{
			Backend:    planner.DNSBackendSystemdResolved,
			TargetLink: "podlaz0",
			Servers:    []string{"1.1.1.1", "9.9.9.9"},
			Action:     planner.DNSActionConfigure,
			Reason:     "use systemd-resolved per-link DNS",
		},
	}
}

type productionTunCommandRunner struct {
	resolvedStatus string
	commands       []string
}

func (r *productionTunCommandRunner) Run(_ context.Context, name string, args ...string) (netexecutor.CommandResult, error) {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	r.commands = append(r.commands, command)
	switch command {
	case "ip -details link show dev podlaz0":
		return netexecutor.CommandResult{Stdout: productionTunLinkForTest}, nil
	case "resolvectl status --no-pager":
		return netexecutor.CommandResult{Stdout: r.resolvedStatus}, nil
	case "resolvectl revert podlaz0",
		"resolvectl dns podlaz0 1.1.1.1 9.9.9.9",
		"resolvectl domain podlaz0 ~.",
		"resolvectl default-route podlaz0 yes":
		return netexecutor.CommandResult{}, nil
	default:
		return netexecutor.CommandResult{ExitCode: 2, Stderr: "unexpected command"}, fmt.Errorf("unexpected command: %s", command)
	}
}

func (r *productionTunCommandRunner) count(command string) int {
	count := 0
	for _, got := range r.commands {
		if got == command {
			count++
		}
	}
	return count
}
