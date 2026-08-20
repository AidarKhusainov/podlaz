package executor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestPrivacyEnvelopeExecutorApplyVerifyAndRemoveExactDynamicTable(t *testing.T) {
	plan := privacyEnvelopePlanForTest("podlaz_pe_001122334455", "192.0.2.10")
	runner := &nftScriptRecordingRunner{recordingRunner: recordingRunner{stdout: privacyEnvelopeListOutputForTest(plan.Table, "192.0.2.10")}}
	exec := PrivacyEnvelopeExecutor{Runner: runner, ScriptDir: t.TempDir()}

	if err := exec.Apply(context.Background(), plan); err != nil {
		t.Fatalf("apply privacy envelope: %v", err)
	}
	if err := exec.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify privacy envelope: %v", err)
	}
	if err := exec.Remove(context.Background(), plan); err != nil {
		t.Fatalf("remove privacy envelope: %v", err)
	}

	if len(runner.commands) != 3 {
		t.Fatalf("expected apply batch, exact verify, exact remove, got %#v", runner.commands)
	}
	if got := runner.commands[1]; !reflect.DeepEqual(got, []string{"nft", "-y", "list", "table", "inet", plan.Table}) {
		t.Fatalf("unexpected verify command: %#v", got)
	}
	if got := runner.commands[2]; !reflect.DeepEqual(got, []string{"nft", "delete", "table", "inet", plan.Table}) {
		t.Fatalf("unexpected remove command: %#v", got)
	}
	for _, want := range []string{
		"add table inet " + plan.Table,
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

func TestPrivacyEnvelopeExecutorReplaceIsOneAtomicNftBatch(t *testing.T) {
	oldPlan := privacyEnvelopePlanForTest("podlaz_pe_001122334455", "192.0.2.10")
	newPlan := privacyEnvelopePlanForTest("podlaz_pe_001122334455", "198.51.100.20")
	runner := &nftScriptRecordingRunner{}
	exec := PrivacyEnvelopeExecutor{Runner: runner, ScriptDir: t.TempDir()}

	if err := exec.Replace(context.Background(), oldPlan, newPlan); err != nil {
		t.Fatalf("replace privacy envelope: %v", err)
	}
	if len(runner.commands) != 1 || len(runner.commands[0]) != 3 || runner.commands[0][0] != "nft" || runner.commands[0][1] != "-f" {
		t.Fatalf("replacement must be one nft batch, got %#v", runner.commands)
	}
	deleteAt := strings.Index(runner.script, "delete table inet "+oldPlan.Table)
	addAt := strings.Index(runner.script, "add table inet "+newPlan.Table)
	newEndpointAt := strings.Index(runner.script, "ip daddr 198.51.100.20")
	if deleteAt < 0 || addAt <= deleteAt || newEndpointAt <= addAt {
		t.Fatalf("replacement batch does not atomically rebuild exact table:\n%s", runner.script)
	}
	if strings.Contains(runner.script, "192.0.2.10") {
		t.Fatalf("replacement must reconstruct only the new exact composition:\n%s", runner.script)
	}
}

func TestPrivacyEnvelopeExecutorBatchFailureNeverRunsCompensatingDelete(t *testing.T) {
	plan := privacyEnvelopePlanForTest("podlaz_pe_001122334455", "192.0.2.10")
	runner := &nftScriptRecordingRunner{recordingRunner: recordingRunner{err: errors.New("injected nft transaction failure")}}
	exec := PrivacyEnvelopeExecutor{Runner: runner, ScriptDir: t.TempDir()}

	if err := exec.Apply(context.Background(), plan); err == nil {
		t.Fatal("expected injected apply failure")
	}
	if len(runner.commands) != 1 || runner.commands[0][0] != "nft" || runner.commands[0][1] != "-f" {
		t.Fatalf("failed atomic apply must not perform compensating mutations, got %#v", runner.commands)
	}
}

func TestPrivacyEnvelopeExecutorVerifyRejectsCompositionDrift(t *testing.T) {
	plan := privacyEnvelopePlanForTest("podlaz_pe_001122334455", "192.0.2.10")
	output := strings.Replace(
		privacyEnvelopeListOutputForTest(plan.Table, "192.0.2.10"),
		`counter reject comment "podlaz:privacy-envelope:block-direct"`,
		`meta l4proto tcp counter accept comment "foreign:extra"\n\t\tcounter reject comment "podlaz:privacy-envelope:block-direct"`,
		1,
	)
	err := (PrivacyEnvelopeExecutor{Runner: &recordingRunner{stdout: output}}).Verify(context.Background(), plan)
	if err == nil {
		t.Fatal("exact verification must reject extra firewall rules")
	}
}

func TestPrivacyEnvelopeExecutorRefusesAmbiguousTargetWithoutMutation(t *testing.T) {
	plan := privacyEnvelopePlanForTest("foreign_table", "192.0.2.10")
	runner := &recordingRunner{}
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

func TestPrivacyEnvelopeExecutorRemoveIsIdempotentOnlyForMissingExactTable(t *testing.T) {
	plan := privacyEnvelopePlanForTest("podlaz_pe_001122334455", "192.0.2.10")
	runner := &recordingRunner{err: errors.New("No such file or directory")}
	if err := (PrivacyEnvelopeExecutor{Runner: runner}).Remove(context.Background(), plan); err != nil {
		t.Fatalf("missing exact envelope must be idempotent: %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("expected one exact removal attempt, got %#v", runner.commands)
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

func privacyEnvelopeListOutputForTest(table, bootstrapIPv4 string) string {
	return `table inet ` + table + ` {
	chain output {
		type filter hook output priority -10; policy accept;
		ip daddr ` + bootstrapIPv4 + ` counter accept comment "podlaz:privacy-envelope:bootstrap"
		oifname "lo" counter accept comment "podlaz:privacy-envelope:loopback"
		oifname "podlaz0" counter accept comment "podlaz:privacy-envelope:tun-egress"
		counter reject comment "podlaz:privacy-envelope:block-direct"
	}
}`
}
