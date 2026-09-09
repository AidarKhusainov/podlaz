package executor

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestPrivacyEnvelopeExecutorApplyVerifyAndRemoveExactDynamicTable(t *testing.T) {
	plan := privacyEnvelopePlanForTest("podlaz_pe_001122334455", "192.0.2.10")
	runner := &privacyEnvelopeRecordingRunner{
		presenceJSON: nftTablesPresenceJSONForTest(plan.Family, plan.Table, 10),
		tableJSON:    privacyEnvelopeJSONForTest(plan.Table, "192.0.2.10"),
	}
	backend := coherentTestMutationBackend(70)
	removeCalls := 0
	backend.removeTable = func(_ context.Context, target nftMutationTarget) error {
		removeCalls++
		if target.Family != plan.Family || target.Table != plan.Table || target.Handle != 10 || target.Generation != 70 {
			t.Fatalf("unexpected verified removal target: %#v", target)
		}
		return nil
	}
	exec := PrivacyEnvelopeExecutor{Runner: runner, ScriptDir: t.TempDir(), mutation: backend}

	if err := exec.Apply(context.Background(), plan); err != nil {
		t.Fatalf("apply privacy envelope: %v", err)
	}
	if err := exec.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify privacy envelope: %v", err)
	}
	if err := exec.Remove(context.Background(), plan); err != nil {
		t.Fatalf("remove privacy envelope: %v", err)
	}
	if removeCalls != 1 {
		t.Fatalf("verified removal calls=%d, want 1", removeCalls)
	}

	wantCommands := [][]string{
		{"nft", "-f", runner.commands[0][2]},
		{"nft", "-j", "list", "table", "inet", plan.Table},
		{"nft", "-j", "list", "tables"},
		{"nft", "-j", "list", "table", "inet", plan.Table},
	}
	if len(runner.commands) != len(wantCommands) {
		t.Fatalf("commands=%#v, want %d commands", runner.commands, len(wantCommands))
	}
	for i := range wantCommands {
		if i == 0 {
			if len(runner.commands[i]) != 3 || runner.commands[i][0] != "nft" || runner.commands[i][1] != "-f" {
				t.Fatalf("unexpected apply command: %#v", runner.commands[i])
			}
			continue
		}
		if !reflect.DeepEqual(runner.commands[i], wantCommands[i]) {
			t.Fatalf("command[%d]=%#v, want %#v", i, runner.commands[i], wantCommands[i])
		}
	}
	for _, want := range []string{
		"create table inet " + plan.Table,
		"add chain inet " + plan.Table + " output { type filter hook output priority -10; policy accept; }",
		`ip daddr 192.0.2.10 counter accept comment "podlaz:privacy-envelope:bootstrap"`,
		`oifname "lo" counter accept comment "podlaz:privacy-envelope:loopback"`,
		`oifname "podlaz0" counter accept comment "podlaz:privacy-envelope:tun-egress"`,
		`counter reject comment "podlaz:privacy-envelope:block-direct"`,
	} {
		if !strings.Contains(runner.script, want) {
			t.Fatalf("privacy envelope apply script missing %q:\n%s", want, runner.script)
		}
	}
}

func TestPrivacyEnvelopeExecutorReplaceUsesFreshVerifiedMutationBackend(t *testing.T) {
	oldPlan := privacyEnvelopePlanForTest("podlaz_pe_001122334455", "192.0.2.10")
	newPlan := privacyEnvelopePlanForTest("podlaz_pe_001122334455", "198.51.100.20")
	runner := &privacyEnvelopeRecordingRunner{
		presenceJSON: nftTablesPresenceJSONForTest(oldPlan.Family, oldPlan.Table, 10),
		tableJSON:    privacyEnvelopeJSONForTest(oldPlan.Table, "192.0.2.10"),
	}
	backend := coherentTestMutationBackend(71)
	replaceCalls := 0
	backend.replaceTable = func(_ context.Context, target nftMutationTarget, replacement planner.TunFirewallPlan) error {
		replaceCalls++
		if target.Handle != 10 || target.Generation != 71 {
			t.Fatalf("unexpected replacement target: %#v", target)
		}
		if replacement.Family != newPlan.Family || replacement.Table != newPlan.Table || !reflect.DeepEqual(replacement.Chains, newPlan.Chains) || !reflect.DeepEqual(replacement.Rules, newPlan.Rules) {
			t.Fatalf("unexpected replacement composition: %#v", replacement)
		}
		return nil
	}
	exec := PrivacyEnvelopeExecutor{Runner: runner, mutation: backend}

	if err := exec.Replace(context.Background(), oldPlan, newPlan); err != nil {
		t.Fatalf("replace privacy envelope: %v", err)
	}
	if replaceCalls != 1 {
		t.Fatalf("replacement calls=%d, want 1", replaceCalls)
	}
	wantCommands := [][]string{
		{"nft", "-j", "list", "tables"},
		{"nft", "-j", "list", "table", "inet", oldPlan.Table},
	}
	if !reflect.DeepEqual(runner.commands, wantCommands) {
		t.Fatalf("replacement observation commands=%#v, want %#v", runner.commands, wantCommands)
	}
	if runner.script != "" {
		t.Fatalf("guarded replacement must not use a separate nft -f script:\n%s", runner.script)
	}
}

func TestPrivacyEnvelopeExecutorBatchFailureNeverRunsCompensatingDelete(t *testing.T) {
	plan := privacyEnvelopePlanForTest("podlaz_pe_001122334455", "192.0.2.10")
	runner := &privacyEnvelopeRecordingRunner{batchErr: errors.New("injected nft transaction failure")}
	exec := PrivacyEnvelopeExecutor{Runner: runner, ScriptDir: t.TempDir()}

	if err := exec.Apply(context.Background(), plan); err == nil {
		t.Fatal("expected injected apply failure")
	}
	if len(runner.commands) != 1 || len(runner.commands[0]) != 3 || runner.commands[0][0] != "nft" || runner.commands[0][1] != "-f" {
		t.Fatalf("failed atomic apply must not perform compensating mutations, got %#v", runner.commands)
	}
}

func TestPrivacyEnvelopeExecutorVerifyRejectsCompositionDrift(t *testing.T) {
	plan := privacyEnvelopePlanForTest("podlaz_pe_001122334455", "192.0.2.10")
	output := strings.Replace(
		privacyEnvelopeJSONForTest(plan.Table, "192.0.2.10"),
		`"comment":"podlaz:privacy-envelope:block-direct"`,
		`"comment":"foreign:replacement"`,
		1,
	)
	err := (PrivacyEnvelopeExecutor{Runner: &privacyEnvelopeRecordingRunner{tableJSON: output}}).Verify(context.Background(), plan)
	if err == nil {
		t.Fatal("exact verification must reject changed ownership comment")
	}
}

func TestPrivacyEnvelopeExecutorRefusesAmbiguousTargetWithoutMutation(t *testing.T) {
	plan := privacyEnvelopePlanForTest("foreign_table", "192.0.2.10")
	runner := &privacyEnvelopeRecordingRunner{}
	exec := PrivacyEnvelopeExecutor{Runner: runner, ScriptDir: t.TempDir()}

	if err := exec.Apply(context.Background(), plan); err == nil {
		t.Fatal("expected non-envelope table rejection")
	}
	if err := exec.Remove(context.Background(), plan); err == nil {
		t.Fatal("expected non-envelope table removal rejection")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("ambiguous target must not be mutated by name resemblance, got %#v", runner.commands)
	}
}

func TestPrivacyEnvelopeExecutorRemoveIsIdempotentOnlyForStructurallyProvenAbsence(t *testing.T) {
	plan := privacyEnvelopePlanForTest("podlaz_pe_001122334455", "192.0.2.10")
	runner := &privacyEnvelopeRecordingRunner{presenceJSON: nftTablesAbsenceJSONForTest()}
	backend := coherentTestMutationBackend(72)
	backend.removeTable = func(context.Context, nftMutationTarget) error {
		t.Fatal("proven absence must not issue a mutation")
		return nil
	}
	if err := (PrivacyEnvelopeExecutor{Runner: runner, mutation: backend}).Remove(context.Background(), plan); err != nil {
		t.Fatalf("structurally proven missing envelope must be idempotent: %v", err)
	}
	want := [][]string{{"nft", "-j", "list", "tables"}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("absence observation commands=%#v, want %#v", runner.commands, want)
	}
}

func privacyEnvelopePlanForTest(table, bootstrapIPv4 string) PrivacyEnvelopePlan {
	return PrivacyEnvelopePlan{
		Family: "inet",
		Table:  table,
		Chains: []planner.TunFirewallChainPlan{{
			Name:     "output",
			Type:     "filter",
			Hook:     "output",
			Priority: -10,
			Policy:   "accept",
			Action:   planner.FirewallActionAdd,
		}},
		Rules: []planner.TunFirewallRulePlan{
			{Chain: "output", Expr: "ip daddr " + bootstrapIPv4, Verdict: planner.FirewallVerdictAccept, Action: planner.FirewallActionAdd, Ownership: "podlaz:privacy-envelope:bootstrap"},
			{Chain: "output", Expr: `oifname "lo"`, Verdict: planner.FirewallVerdictAccept, Action: planner.FirewallActionAdd, Ownership: "podlaz:privacy-envelope:loopback"},
			{Chain: "output", Expr: `oifname "podlaz0"`, Verdict: planner.FirewallVerdictAccept, Action: planner.FirewallActionAdd, Ownership: "podlaz:privacy-envelope:tun-egress"},
			{Chain: "output", Expr: "", Verdict: planner.FirewallVerdictReject, Action: planner.FirewallActionAdd, Ownership: "podlaz:privacy-envelope:block-direct"},
		},
		Reason: "preserve a fail-closed network session privacy boundary",
	}
}

func privacyEnvelopeJSONForTest(table, bootstrapIPv4 string) string {
	return `{"nftables":[
{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},
{"table":{"family":"inet","name":"` + table + `","handle":10}},
{"chain":{"family":"inet","table":"` + table + `","name":"output","handle":1,"type":"filter","hook":"output","prio":-10,"policy":"accept"}},
{"rule":{"family":"inet","table":"` + table + `","chain":"output","handle":1,"expr":[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"` + bootstrapIPv4 + `"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:privacy-envelope:bootstrap"}},
{"rule":{"family":"inet","table":"` + table + `","chain":"output","handle":2,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"lo"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:privacy-envelope:loopback"}},
{"rule":{"family":"inet","table":"` + table + `","chain":"output","handle":3,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"podlaz0"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:privacy-envelope:tun-egress"}},
{"rule":{"family":"inet","table":"` + table + `","chain":"output","handle":4,"expr":[{"counter":{"packets":0,"bytes":0}},{"reject":{"type":"icmpx","expr":"port-unreachable"}}],"comment":"podlaz:privacy-envelope:block-direct"}}
]}`
}

type privacyEnvelopeRecordingRunner struct {
	commands     [][]string
	script       string
	presenceJSON string
	tableJSON    string
	batchErr     error
}

func (r *privacyEnvelopeRecordingRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := append([]string{name}, args...)
	r.commands = append(r.commands, command)
	if name != "nft" {
		return CommandResult{ExitCode: 1}, errors.New("unexpected command")
	}
	if len(args) == 2 && args[0] == "-f" {
		data, err := os.ReadFile(args[1])
		if err != nil {
			return CommandResult{ExitCode: 1, Stderr: err.Error()}, err
		}
		r.script = string(data)
		if r.batchErr != nil {
			return CommandResult{ExitCode: 1, Stderr: r.batchErr.Error()}, r.batchErr
		}
		return CommandResult{}, nil
	}
	if reflect.DeepEqual(args, []string{"-j", "list", "tables"}) {
		return CommandResult{Stdout: r.presenceJSON}, nil
	}
	if len(args) == 5 && reflect.DeepEqual(args[:4], []string{"-j", "list", "table", "inet"}) {
		return CommandResult{Stdout: r.tableJSON}, nil
	}
	return CommandResult{ExitCode: 1}, errors.New("unexpected nft command")
}
