package executor

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestNftablesExecutorApplyVerifyAndRollbackCommands(t *testing.T) {
	plan := firewallPlanForTest()
	runner := &nftScriptRecordingRunner{recordingRunner: recordingRunner{stdout: nftablesListOutputForTest()}}
	exec := NftablesExecutor{Runner: runner, ScriptDir: t.TempDir()}

	step, err := exec.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply nftables: %v", err)
	}
	if step.Kind != "nftables" || step.Target != "inet podlaz" || step.Owner != OwnerFirewall {
		t.Fatalf("unexpected nftables step: %#v", step)
	}
	if err := exec.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify nftables: %v", err)
	}
	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback nftables: %v", err)
	}

	if len(runner.commands) != 3 {
		t.Fatalf("expected apply batch, verify, rollback commands, got %#v", runner.commands)
	}
	if len(runner.commands[0]) != 3 || runner.commands[0][0] != "nft" || runner.commands[0][1] != "-f" {
		t.Fatalf("expected nft batch apply command, got %#v", runner.commands[0])
	}
	wantTail := [][]string{
		{"nft", "-y", "list", "table", "inet", "podlaz"},
		{"nft", "delete", "table", "inet", "podlaz"},
	}
	if !reflect.DeepEqual(runner.commands[1:], wantTail) {
		t.Fatalf("unexpected commands after apply:\nwant %#v\n got %#v", wantTail, runner.commands[1:])
	}
	for _, want := range []string{
		"add table inet podlaz",
		"add chain inet podlaz output { type filter hook output priority 0; policy accept; }",
		`add rule inet podlaz output ip daddr 203.0.113.10 counter accept comment "podlaz:firewall:server-bypass"`,
		`add rule inet podlaz output oifname "lo" counter accept comment "podlaz:firewall:loopback"`,
		`add rule inet podlaz output oifname "podlaz0" counter accept comment "podlaz:firewall:tun-egress"`,
		`add rule inet podlaz output oifname != "podlaz0" counter reject comment "podlaz:firewall:kill-switch"`,
	} {
		if !strings.Contains(runner.script, want) {
			t.Fatalf("expected batch script to contain %q, got:\n%s", want, runner.script)
		}
	}
}

func TestNftStringLiteralQuotesAndEscapesForNftCLI(t *testing.T) {
	tests := map[string]string{
		`podlaz:firewall:server-bypass`:      `"podlaz:firewall:server-bypass"`,
		`podlaz:firewall:owner "quoted"`:     `"podlaz:firewall:owner \"quoted\""`,
		`podlaz:firewall:owner\with\slashes`: `"podlaz:firewall:owner\\with\\slashes"`,
	}

	for input, want := range tests {
		if got := nftStringLiteral(input); got != want {
			t.Fatalf("nftStringLiteral(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNftablesExecutorApplyUsesAtomicBatchAndDoesNotRollbackOnBatchFailure(t *testing.T) {
	plan := firewallPlanForTest()
	runner := &nftScriptRecordingRunner{recordingRunner: recordingRunner{err: errors.New("injected nft batch failure")}}
	_, err := (NftablesExecutor{Runner: runner, ScriptDir: t.TempDir()}).Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("expected batch apply failure")
	}
	if len(runner.commands) != 1 || len(runner.commands[0]) != 3 || runner.commands[0][0] != "nft" || runner.commands[0][1] != "-f" {
		t.Fatalf("expected only nft batch apply command without rollback side effect, got %#v", runner.commands)
	}
	if !strings.Contains(runner.script, "add table inet podlaz") || !strings.Contains(runner.script, `comment "podlaz:firewall:kill-switch"`) {
		t.Fatalf("expected complete batch script to be produced before apply failure, got:\n%s", runner.script)
	}
}

func TestNftablesExecutorRejectsBlockedOrNonOwnedPlan(t *testing.T) {
	blocked := firewallPlanForTest()
	blocked.TableAction = planner.FirewallActionBlocked
	if _, err := (NftablesExecutor{Runner: &recordingRunner{}}).Apply(context.Background(), blocked); err == nil {
		t.Fatal("expected blocked firewall plan failure")
	}

	nonOwnedRule := firewallPlanForTest()
	nonOwnedRule.Rules[0].Ownership = "other-project"
	if _, err := (NftablesExecutor{Runner: &recordingRunner{}}).Apply(context.Background(), nonOwnedRule); err == nil {
		t.Fatal("expected non-podlaz rule owner failure")
	}

	nonOwnedTarget := firewallPlanForTest()
	nonOwnedTarget.Table = "filter"
	if _, err := (NftablesExecutor{Runner: &recordingRunner{}}).Apply(context.Background(), nonOwnedTarget); err == nil {
		t.Fatal("expected non-podlaz table failure")
	}
}

func TestNftablesExecutorRollbackRejectsNonOwnedTarget(t *testing.T) {
	plan := firewallPlanForTest()
	plan.Table = "filter"
	runner := &recordingRunner{}
	err := (NftablesExecutor{Runner: runner}).Rollback(context.Background(), plan)
	if err == nil {
		t.Fatal("expected rollback to reject non-owned nftables target")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("rollback must not execute nft for non-owned target, got %#v", runner.commands)
	}
}

func TestNftablesExecutorRollbackIsIdempotentWhenTableIsMissing(t *testing.T) {
	plan := firewallPlanForTest()
	runner := &recordingRunner{err: errors.New("No such file or directory")}
	if err := (NftablesExecutor{Runner: runner}).Rollback(context.Background(), plan); err != nil {
		t.Fatalf("expected missing table rollback to be ignored: %v", err)
	}
	want := []string{"nft", "delete", "table", "inet", "podlaz"}
	if !reflect.DeepEqual(runner.commands[0], want) {
		t.Fatalf("unexpected rollback command: %#v", runner.commands[0])
	}
}

func TestNftablesExecutorVerifyRequiresOwnedRules(t *testing.T) {
	plan := firewallPlanForTest()
	output := strings.ReplaceAll(nftablesListOutputForTest(), planner.FirewallKillSwitchOwner, "missing-owner")
	err := (NftablesExecutor{Runner: &recordingRunner{stdout: output}}).Verify(context.Background(), plan)
	if err == nil {
		t.Fatal("expected verify failure when owned kill-switch rule is missing")
	}
}

func TestNftablesExecutorVerifyMatchesRuleFieldsOnSameLine(t *testing.T) {
	plan := firewallPlanForTest()
	output := `table inet podlaz {
	chain output {
		type filter hook output priority 0; policy accept;
		oifname != "podlaz0" counter reject comment "other-project"
		ip daddr 203.0.113.10 counter accept comment "podlaz:firewall:server-bypass"
		oifname "lo" counter accept comment "podlaz:firewall:loopback"
		oifname "podlaz0" counter accept comment "podlaz:firewall:tun-egress"
		meta l4proto tcp counter reject comment "podlaz:firewall:kill-switch"
	}
}`
	err := (NftablesExecutor{Runner: &recordingRunner{stdout: output}}).Verify(context.Background(), plan)
	if err == nil {
		t.Fatal("expected verify failure when expression, verdict, and owner appear on different rules")
	}
}

func firewallPlanForTest() planner.TunFirewallPlan {
	return planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      snapshot.DefaultNFTFamily,
		Table:       snapshot.DefaultNFTTable,
		TableAction: planner.FirewallTableAction,
		Chains: []planner.TunFirewallChainPlan{{
			Name:     planner.FirewallOutputChain,
			Type:     planner.FirewallChainTypeFilter,
			Hook:     planner.FirewallOutputHook,
			Priority: planner.FirewallOutputPriority,
			Policy:   planner.FirewallDefaultChainPolicy,
			Action:   planner.FirewallTableAction,
		}},
		Rules: []planner.TunFirewallRulePlan{
			{Chain: planner.FirewallOutputChain, Expr: "ip daddr 203.0.113.10", Verdict: planner.FirewallVerdictAccept, Action: planner.FirewallActionAdd, Ownership: planner.FirewallServerBypassOwner, RollbackKey: planner.FirewallServerBypassKey},
			{Chain: planner.FirewallOutputChain, Expr: "oifname \"lo\"", Verdict: planner.FirewallVerdictAccept, Action: planner.FirewallActionAdd, Ownership: planner.FirewallLoopbackOwner, RollbackKey: planner.FirewallLoopbackKey},
			{Chain: planner.FirewallOutputChain, Expr: "oifname \"podlaz0\"", Verdict: planner.FirewallVerdictAccept, Action: planner.FirewallActionAdd, Ownership: planner.FirewallTunEgressOwner, RollbackKey: planner.FirewallTunEgressKey},
			{Chain: planner.FirewallOutputChain, Expr: "oifname != \"podlaz0\"", Verdict: planner.FirewallVerdictReject, Action: planner.FirewallActionAdd, Ownership: planner.FirewallKillSwitchOwner, RollbackKey: planner.FirewallKillSwitchKey},
		},
		KillSwitch: planner.TunKillSwitchPlan{Policy: planner.KillSwitchPolicySoft},
		Reason:     "create a podlaz-owned nftables table",
		Rollback:   planner.FirewallRollbackRemove,
	}
}

func nftablesListOutputForTest() string {
	return `table inet podlaz {
	chain output {
		type filter hook output priority 0; policy accept;
		ip daddr 203.0.113.10 counter accept comment "podlaz:firewall:server-bypass"
		oifname "lo" counter accept comment "podlaz:firewall:loopback"
		oifname "podlaz0" counter accept comment "podlaz:firewall:tun-egress"
		oifname != "podlaz0" counter reject comment "podlaz:firewall:kill-switch"
	}
}`
}

type nftScriptRecordingRunner struct {
	recordingRunner
	script string
}

func (r *nftScriptRecordingRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	if name == "nft" && len(args) == 2 && args[0] == "-f" {
		data, err := os.ReadFile(args[1])
		if err != nil {
			return CommandResult{ExitCode: 1, Stderr: err.Error()}, err
		}
		r.script = string(data)
	}
	return r.recordingRunner.Run(ctx, name, args...)
}
