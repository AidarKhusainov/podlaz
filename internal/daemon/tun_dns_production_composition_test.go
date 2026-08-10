package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
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
	if err == nil || !strings.Contains(err.Error(), "DNS servers mismatch") {
		t.Fatalf("production composition must fail closed on exact DNS mismatch, got %v", err)
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
	if len(warnings) != 0 {
		t.Fatalf("rollback left unreadable transaction state: %#v", warnings)
	}
	for _, summary := range summaries {
		if summary.RequiresCleanup {
			t.Fatalf("rollback left a cleanup-required transaction/startup-scan blocker: %#v", summary)
		}
	}

	manager := NewXrayManager(runtimeDir)
	snapshot := netsnapshot.Snapshot{StaleResources: manager.transactionFileStaleResources()}
	if err := preflightTunOwnership(snapshot, api.HandoffBlock); err != nil {
		t.Fatalf("immediate TUN retry was blocked by stale production preflight state: %v", err)
	}
}

func productionDNSPlanForTest() planner.TunPlan {
	plan := planner.TunPlan{
		Mode:      planner.ModeTun,
		ProfileID: "example-profile",
		TunDevice: planner.TunDevicePlan{Name: netsnapshot.DefaultTunName, MTU: planner.DefaultTunMTU, Action: "verify"},
		TunAddress: planner.TunAddressPlan{
			Family:            "ipv4",
			Interface:         netsnapshot.DefaultTunName,
			CIDR:              planner.DefaultTunIPv4CIDR,
			Scope:             "global",
			Action:            planner.TunAddressActionAssign,
			Owner:             netexecutor.OwnerTunAddress,
			RollbackKey:       netsnapshot.DefaultTunName + "/" + planner.DefaultTunIPv4CIDR,
			LinkIndex:         7,
			LinkKind:          "tun",
			AppearedAfterCore: true,
		},
		Routes: []planner.TunRoutePlan{
			{Family: "ipv4", Destination: planner.IPv4DefaultRoute, Table: planner.TunRoutingTable, Interface: netsnapshot.DefaultTunName, Action: "add"},
			{Family: "ipv4", Destination: "203.0.113.10/32", Table: planner.MainRoutingTable, Interface: "eth0", Gateway: "192.0.2.1", Action: "add"},
		},
		PolicyRules: []planner.TunPolicyRulePlan{
			{Family: "ipv4", Priority: planner.ServerRulePriority, Selector: "to 203.0.113.10/32", Table: planner.MainRoutingTable, Action: "add"},
			{Family: "ipv4", Priority: planner.TunRulePriority, Selector: planner.IPv4DefaultSelector, Table: planner.TunRoutingTable, Action: "add"},
		},
		ServerBypass: planner.TunRoutePlan{Family: "ipv4", Destination: "203.0.113.10/32", Table: planner.MainRoutingTable, Interface: "eth0", Gateway: "192.0.2.1", Action: "add"},
		DNS: planner.TunDNSPlan{
			Backend:    planner.DNSBackendSystemdResolved,
			TargetLink: netsnapshot.DefaultTunName,
			Servers:    []string{"1.1.1.1", "9.9.9.9"},
			Action:     planner.DNSActionConfigure,
		},
		Firewall: productionFirewallPlanForTest(),
	}
	return plan
}

func productionFirewallPlanForTest() planner.TunFirewallPlan {
	return planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      netsnapshot.DefaultNFTFamily,
		Table:       netsnapshot.DefaultNFTTable,
		TableAction: planner.FirewallTableAction,
		Chains: []planner.TunFirewallChainPlan{{
			Name: planner.FirewallOutputChain, Type: planner.FirewallChainTypeFilter, Hook: planner.FirewallOutputHook, Priority: planner.FirewallOutputPriority, Policy: planner.FirewallDefaultChainPolicy, Action: planner.FirewallTableAction,
		}},
		Rules: []planner.TunFirewallRulePlan{
			{Chain: planner.FirewallOutputChain, Expr: "ip daddr 203.0.113.10", Verdict: planner.FirewallVerdictAccept, Action: planner.FirewallActionAdd, Ownership: planner.FirewallServerBypassOwner, RollbackKey: planner.FirewallServerBypassKey},
			{Chain: planner.FirewallOutputChain, Expr: `oifname "lo"`, Verdict: planner.FirewallVerdictAccept, Action: planner.FirewallActionAdd, Ownership: planner.FirewallLoopbackOwner, RollbackKey: planner.FirewallLoopbackKey},
			{Chain: planner.FirewallOutputChain, Expr: `oifname "podlaz0"`, Verdict: planner.FirewallVerdictAccept, Action: planner.FirewallActionAdd, Ownership: planner.FirewallTunEgressOwner, RollbackKey: planner.FirewallTunEgressKey},
		},
	}
}

type productionTunCommandRunner struct {
	commands       []string
	resolvedStatus string
	nftTable       bool
}

func (r *productionTunCommandRunner) Run(_ context.Context, name string, args ...string) (netexecutor.CommandResult, error) {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	r.commands = append(r.commands, command)

	switch {
	case name == "ip" && (strings.Contains(command, "-details link show dev podlaz0") || strings.Contains(command, "-details -o link show dev podlaz0")):
		return netexecutor.CommandResult{Stdout: productionTunLinkForTest, ExitCode: 0}, nil
	case name == "ip" && (strings.Contains(command, "-4 -o address show dev podlaz0") || strings.Contains(command, "-4 -o addr show dev podlaz0 scope global")):
		return netexecutor.CommandResult{Stdout: "7: podlaz0    inet 198.18.0.1/32 scope global podlaz0", ExitCode: 0}, nil
	case name == "ip" && strings.Contains(command, "-4 addr add"):
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case name == "ip" && strings.Contains(command, "-4 addr del"):
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case name == "ip" && strings.Contains(command, "-4 route show table"):
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case name == "ip" && strings.Contains(command, "-4 route add"):
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case name == "ip" && strings.Contains(command, "-4 route del"):
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case name == "ip" && strings.Contains(command, "-4 route flush cache"):
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case name == "ip" && strings.Contains(command, "-4 rule show priority"):
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case name == "ip" && strings.Contains(command, "-4 rule add"):
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case name == "ip" && strings.Contains(command, "-4 rule del"):
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case name == "resolvectl" && strings.HasPrefix(strings.Join(args, " "), "status --no-pager"):
		return netexecutor.CommandResult{Stdout: r.resolvedStatus, ExitCode: 0}, nil
	case name == "resolvectl" && strings.HasPrefix(strings.Join(args, " "), "revert podlaz0"):
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case name == "resolvectl":
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case name == "nft" && len(args) == 2 && args[0] == "-f":
		r.nftTable = true
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case name == "nft" && strings.HasPrefix(strings.Join(args, " "), "-y list table inet podlaz"):
		if !r.nftTable {
			return netexecutor.CommandResult{ExitCode: 1, Stderr: "No such file or directory"}, fmt.Errorf("nft table absent")
		}
		return netexecutor.CommandResult{Stdout: productionNFTListOutputForTest(), ExitCode: 0}, nil
	case name == "nft" && strings.HasPrefix(strings.Join(args, " "), "delete table inet podlaz"):
		r.nftTable = false
		return netexecutor.CommandResult{ExitCode: 0}, nil
	default:
		return netexecutor.CommandResult{ExitCode: 0}, nil
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

func productionNFTListOutputForTest() string {
	return `table inet podlaz {
	chain output {
		type filter hook output priority 0; policy accept;
		ip daddr 203.0.113.10 counter accept comment "podlaz:firewall:server-bypass"
		oifname "lo" counter accept comment "podlaz:firewall:loopback"
		oifname "podlaz0" counter accept comment "podlaz:firewall:tun-egress"
	}
}`
}
