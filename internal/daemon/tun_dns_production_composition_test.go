package daemon

import (
	"context"
	"fmt"
	"strconv"
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
	runner := newProductionTunCommandRunner(productionResolvedInactiveScopeForTest)
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

func TestProductionTunCommandRunnerRejectsUnexpectedCommand(t *testing.T) {
	runner := newProductionTunCommandRunner(productionResolvedInactiveScopeForTest)
	result, err := runner.Run(context.Background(), "ip", "-4", "unexpected")
	if err == nil || result.ExitCode == 0 {
		t.Fatalf("unexpected production command must fail closed: result=%#v err=%v", result, err)
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
	return planner.TunPlan{
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
	tunAddress     bool
	routes         map[string]string
	rules          map[int]string
}

func newProductionTunCommandRunner(resolvedStatus string) *productionTunCommandRunner {
	return &productionTunCommandRunner{
		resolvedStatus: resolvedStatus,
		routes:         make(map[string]string),
		rules:          make(map[int]string),
	}
}

func (r *productionTunCommandRunner) Run(_ context.Context, name string, args ...string) (netexecutor.CommandResult, error) {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	r.commands = append(r.commands, command)

	if name == "ip" {
		return r.runIP(args, command)
	}
	if name == "resolvectl" {
		return r.runResolvectl(args, command)
	}
	if name == "nft" {
		return r.runNFT(args, command)
	}
	return unexpectedProductionCommand(command)
}

func (r *productionTunCommandRunner) runIP(args []string, command string) (netexecutor.CommandResult, error) {
	switch command {
	case "ip -details link show dev podlaz0", "ip -details -o link show dev podlaz0":
		return netexecutor.CommandResult{Stdout: productionTunLinkForTest, ExitCode: 0}, nil
	case "ip -4 -o address show dev podlaz0":
		if !r.tunAddress {
			return netexecutor.CommandResult{ExitCode: 0}, nil
		}
		return netexecutor.CommandResult{Stdout: "7: podlaz0    inet 198.18.0.1/32 scope global podlaz0", ExitCode: 0}, nil
	case "ip -4 address replace 198.18.0.1/32 dev podlaz0":
		r.tunAddress = true
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case "ip -4 address del 198.18.0.1/32 dev podlaz0":
		r.tunAddress = false
		return netexecutor.CommandResult{ExitCode: 0}, nil
	case "ip link set dev podlaz0 up", "ip -4 route flush cache":
		return netexecutor.CommandResult{ExitCode: 0}, nil
	}

	if len(args) >= 6 && args[0] == "-4" && args[1] == "route" {
		return r.runIPRoute(args, command)
	}
	if len(args) >= 5 && args[0] == "-4" && args[1] == "rule" {
		return r.runIPRule(args, command)
	}
	return unexpectedProductionCommand(command)
}

func (r *productionTunCommandRunner) runIPRoute(args []string, command string) (netexecutor.CommandResult, error) {
	op := args[2]
	switch op {
	case "show":
		if len(args) != 6 || args[3] != "table" {
			return unexpectedProductionCommand(command)
		}
		key := args[4] + " " + args[5]
		return netexecutor.CommandResult{Stdout: r.routes[key], ExitCode: 0}, nil
	case "add", "del":
		key, line, ok := productionRouteState(args)
		if !ok {
			return unexpectedProductionCommand(command)
		}
		if op == "add" {
			r.routes[key] = line
		} else {
			delete(r.routes, key)
		}
		return netexecutor.CommandResult{ExitCode: 0}, nil
	default:
		return unexpectedProductionCommand(command)
	}
}

func productionRouteState(args []string) (key, line string, ok bool) {
	if len(args) < 7 || args[0] != "-4" || args[1] != "route" || (args[2] != "add" && args[2] != "del") {
		return "", "", false
	}
	destination := args[3]
	table := ""
	gateway := ""
	device := ""
	for i := 4; i+1 < len(args); i += 2 {
		switch args[i] {
		case "via":
			gateway = args[i+1]
		case "dev":
			device = args[i+1]
		case "table":
			table = args[i+1]
		default:
			return "", "", false
		}
	}
	if table == "" || device == "" {
		return "", "", false
	}
	fields := []string{destination}
	if gateway != "" {
		fields = append(fields, "via", gateway)
	}
	fields = append(fields, "dev", device)
	return table + " " + destination, strings.Join(fields, " "), true
}

func (r *productionTunCommandRunner) runIPRule(args []string, command string) (netexecutor.CommandResult, error) {
	op := args[2]
	if op == "show" {
		if len(args) != 5 || args[3] != "priority" {
			return unexpectedProductionCommand(command)
		}
		priority, err := strconv.Atoi(args[4])
		if err != nil {
			return unexpectedProductionCommand(command)
		}
		return netexecutor.CommandResult{Stdout: r.rules[priority], ExitCode: 0}, nil
	}
	if op != "add" && op != "del" || len(args) < 8 || args[3] != "priority" {
		return unexpectedProductionCommand(command)
	}
	priority, err := strconv.Atoi(args[4])
	if err != nil {
		return unexpectedProductionCommand(command)
	}
	lookup := -1
	for i := 5; i < len(args); i++ {
		if args[i] == "lookup" {
			lookup = i
			break
		}
	}
	if lookup <= 5 || lookup+1 != len(args)-1 {
		return unexpectedProductionCommand(command)
	}
	if op == "add" {
		r.rules[priority] = fmt.Sprintf("%d: %s lookup %s", priority, strings.Join(args[5:lookup], " "), args[lookup+1])
	} else {
		delete(r.rules, priority)
	}
	return netexecutor.CommandResult{ExitCode: 0}, nil
}

func (r *productionTunCommandRunner) runResolvectl(args []string, command string) (netexecutor.CommandResult, error) {
	switch command {
	case "resolvectl status --no-pager":
		return netexecutor.CommandResult{Stdout: r.resolvedStatus, ExitCode: 0}, nil
	case "resolvectl revert podlaz0",
		"resolvectl dns podlaz0 1.1.1.1 9.9.9.9",
		"resolvectl domain podlaz0 ~.",
		"resolvectl default-route podlaz0 yes":
		return netexecutor.CommandResult{ExitCode: 0}, nil
	default:
		return unexpectedProductionCommand(command)
	}
}

func (r *productionTunCommandRunner) runNFT(args []string, command string) (netexecutor.CommandResult, error) {
	if len(args) == 2 && args[0] == "-f" && strings.TrimSpace(args[1]) != "" {
		r.nftTable = true
		return netexecutor.CommandResult{ExitCode: 0}, nil
	}
	switch command {
	case "nft -y list table inet podlaz":
		if !r.nftTable {
			return netexecutor.CommandResult{ExitCode: 1, Stderr: "No such file or directory"}, fmt.Errorf("nft table absent")
		}
		return netexecutor.CommandResult{Stdout: productionNFTListOutputForTest(), ExitCode: 0}, nil
	case "nft delete table inet podlaz":
		r.nftTable = false
		return netexecutor.CommandResult{ExitCode: 0}, nil
	default:
		return unexpectedProductionCommand(command)
	}
}

func unexpectedProductionCommand(command string) (netexecutor.CommandResult, error) {
	return netexecutor.CommandResult{ExitCode: 127, Stderr: "unexpected command"}, fmt.Errorf("unexpected production command %q", command)
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
