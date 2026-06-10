package executor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
	"github.com/AidarKhusainov/tunwarden/internal/network/snapshot"
)

func TestNftablesExecutorApplyVerifyAndRollbackCommands(t *testing.T) {
	plan := firewallPlanForTest()
	runner := &recordingRunner{stdout: nftablesListOutputForTest()}
	exec := NftablesExecutor{Runner: runner}

	step, err := exec.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply nftables: %v", err)
	}
	if step.Kind != "nftables" || step.Target != "inet tunwarden" || step.Owner != OwnerFirewall {
		t.Fatalf("unexpected nftables step: %#v", step)
	}
	if err := exec.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify nftables: %v", err)
	}
	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback nftables: %v", err)
	}

	want := [][]string{
		{"nft", "add", "table", "inet", "tunwarden"},
		{"nft", "add", "chain", "inet", "tunwarden", "output", "{", "type", "filter", "hook", "output", "priority", "0", ";", "policy", "accept", ";", "}"},
		{"nft", "add", "rule", "inet", "tunwarden", "output", "ip", "daddr", "203.0.113.10", "counter", "comment", `"` + planner.FirewallServerBypassOwner + `"`, "accept"},
		{"nft", "add", "rule", "inet", "tunwarden", "output", "oifname", "tunwarden0", "counter", "comment", `"` + planner.FirewallTunEgressOwner + `"`, "accept"},
		{"nft", "add", "rule", "inet", "tunwarden", "output", "oifname", "!=", "tunwarden0", "counter", "comment", `"` + planner.FirewallKillSwitchOwner + `"`, "reject"},
		{"nft", "list", "table", "inet", "tunwarden"},
		{"nft", "delete", "table", "inet", "tunwarden"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", want, runner.commands)
	}
}

func TestNftStringLiteralQuotesAndEscapesForNftCLI(t *testing.T) {
	tests := map[string]string{
		`tunwarden:firewall:server-bypass`:      `"tunwarden:firewall:server-bypass"`,
		`tunwarden:firewall:owner "quoted"`:     `"tunwarden:firewall:owner \"quoted\""`,
		`tunwarden:firewall:owner\with\slashes`: `"tunwarden:firewall:owner\\with\\slashes"`,
	}

	for input, want := range tests {
		if got := nftStringLiteral(input); got != want {
			t.Fatalf("nftStringLiteral(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNftablesExecutorRollsBackTableOnPartialApplyFailure(t *testing.T) {
	plan := firewallPlanForTest()
	runner := &failingNftRunner{failOn: []string{"add", "rule"}}
	_, err := (NftablesExecutor{Runner: runner}).Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("expected rule apply failure")
	}

	wantLast := []string{"nft", "delete", "table", "inet", "tunwarden"}
	if len(runner.commands) < 2 || !reflect.DeepEqual(runner.commands[len(runner.commands)-1], wantLast) {
		t.Fatalf("expected final cleanup command %v, got %#v", wantLast, runner.commands)
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
		t.Fatal("expected non-TunWarden rule owner failure")
	}

	nonOwnedTarget := firewallPlanForTest()
	nonOwnedTarget.Table = "filter"
	if _, err := (NftablesExecutor{Runner: &recordingRunner{}}).Apply(context.Background(), nonOwnedTarget); err == nil {
		t.Fatal("expected non-TunWarden table failure")
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
	want := []string{"nft", "delete", "table", "inet", "tunwarden"}
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
	output := `table inet tunwarden {
	chain output {
		type filter hook output priority 0; policy accept;
		oifname != "tunwarden0" counter comment "other-project" reject
		ip daddr 203.0.113.10 counter comment "tunwarden:firewall:server-bypass" accept
		oifname "tunwarden0" counter comment "tunwarden:firewall:tun-egress" accept
		meta l4proto tcp counter comment "tunwarden:firewall:kill-switch" reject
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
			{Chain: planner.FirewallOutputChain, Expr: "oifname \"tunwarden0\"", Verdict: planner.FirewallVerdictAccept, Action: planner.FirewallActionAdd, Ownership: planner.FirewallTunEgressOwner, RollbackKey: planner.FirewallTunEgressKey},
			{Chain: planner.FirewallOutputChain, Expr: "oifname != \"tunwarden0\"", Verdict: planner.FirewallVerdictReject, Action: planner.FirewallActionAdd, Ownership: planner.FirewallKillSwitchOwner, RollbackKey: planner.FirewallKillSwitchKey},
		},
		KillSwitch: planner.TunKillSwitchPlan{Policy: planner.KillSwitchPolicySoft},
		Reason:     "create a TunWarden-owned nftables table",
		Rollback:   planner.FirewallRollbackRemove,
	}
}

func nftablesListOutputForTest() string {
	return `table inet tunwarden {
	chain output {
		type filter hook output priority 0; policy accept;
		ip daddr 203.0.113.10 counter comment "tunwarden:firewall:server-bypass" accept
		oifname "tunwarden0" counter comment "tunwarden:firewall:tun-egress" accept
		oifname != "tunwarden0" counter comment "tunwarden:firewall:kill-switch" reject
	}
}`
}

type failingNftRunner struct {
	commands [][]string
	failOn   []string
}

func (r *failingNftRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := append([]string{name}, args...)
	r.commands = append(r.commands, command)
	if name == "nft" && len(args) >= len(r.failOn) {
		matches := true
		for i, token := range r.failOn {
			if args[i] != token {
				matches = false
				break
			}
		}
		if matches {
			return CommandResult{ExitCode: 1, Stderr: "injected nft failure"}, errors.New("injected nft failure")
		}
	}
	return CommandResult{}, nil
}
