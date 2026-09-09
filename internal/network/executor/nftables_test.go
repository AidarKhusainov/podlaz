package executor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestNftablesExecutorApplyVerifyAndRollbackCommands(t *testing.T) {
	plan := firewallPlanForTest()
	runner := &privacyEnvelopeRecordingRunner{
		presenceJSON: nftTablesPresenceJSONForTest("inet", "podlaz", 21),
		tableJSON:    nftablesJSONForTest(),
	}
	backend := coherentTestMutationBackend(81)
	removeCalls := 0
	backend.removeTable = func(_ context.Context, target nftMutationTarget) error {
		removeCalls++
		if target.Family != "inet" || target.Table != "podlaz" || target.Handle != 21 || target.Generation != 81 {
			t.Fatalf("unexpected rollback target: %#v", target)
		}
		runner.presenceJSON = nftTablesAbsenceJSONForTest()
		return nil
	}
	exec := NftablesExecutor{Runner: runner, ScriptDir: t.TempDir(), mutation: backend}

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
	if removeCalls != 1 {
		t.Fatalf("guarded rollback calls=%d, want 1", removeCalls)
	}

	if len(runner.commands) != 5 {
		t.Fatalf("expected apply, verify, exact rollback observations and absence proof, got %#v", runner.commands)
	}
	if len(runner.commands[0]) != 3 || runner.commands[0][0] != "nft" || runner.commands[0][1] != "-f" {
		t.Fatalf("expected nft batch apply command, got %#v", runner.commands[0])
	}
	wantTail := [][]string{
		{"nft", "-j", "list", "table", "inet", "podlaz"},
		{"nft", "-j", "list", "tables"},
		{"nft", "-j", "list", "table", "inet", "podlaz"},
		{"nft", "-j", "list", "tables"},
	}
	if !reflect.DeepEqual(runner.commands[1:], wantTail) {
		t.Fatalf("unexpected commands after apply:\nwant %#v\n got %#v", wantTail, runner.commands[1:])
	}
	for _, want := range []string{
		"create table inet podlaz",
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
	runner := &privacyEnvelopeRecordingRunner{batchErr: errors.New("injected nft batch failure")}
	_, err := (NftablesExecutor{Runner: runner, ScriptDir: t.TempDir()}).Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("expected batch apply failure")
	}
	if len(runner.commands) != 1 || len(runner.commands[0]) != 3 || runner.commands[0][0] != "nft" || runner.commands[0][1] != "-f" {
		t.Fatalf("expected only nft batch apply command without rollback side effect, got %#v", runner.commands)
	}
	if !strings.Contains(runner.script, "create table inet podlaz") || !strings.Contains(runner.script, `comment "podlaz:firewall:kill-switch"`) {
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

func TestNftablesExecutorRollbackIsIdempotentWhenTableIsStructurallyAbsent(t *testing.T) {
	plan := firewallPlanForTest()
	runner := &privacyEnvelopeRecordingRunner{presenceJSON: nftTablesAbsenceJSONForTest()}
	backend := coherentTestMutationBackend(82)
	backend.removeTable = func(context.Context, nftMutationTarget) error {
		t.Fatal("proven absence must not issue rollback mutation")
		return nil
	}
	if err := (NftablesExecutor{Runner: runner, mutation: backend}).Rollback(context.Background(), plan); err != nil {
		t.Fatalf("expected structured absence rollback to be idempotent: %v", err)
	}
	want := [][]string{{"nft", "-j", "list", "tables"}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("absence observation commands=%#v, want %#v", runner.commands, want)
	}
}

func TestNftablesExecutorVerifyRequiresOwnedRules(t *testing.T) {
	plan := firewallPlanForTest()
	output := strings.ReplaceAll(nftablesJSONForTest(), planner.FirewallKillSwitchOwner, "missing-owner")
	err := (NftablesExecutor{Runner: &privacyEnvelopeRecordingRunner{tableJSON: output}}).Verify(context.Background(), plan)
	if err == nil {
		t.Fatal("expected verify failure when owned kill-switch rule is missing")
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
