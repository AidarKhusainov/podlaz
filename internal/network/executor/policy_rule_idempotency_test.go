package executor

import (
	"context"
	"reflect"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestIPPolicyRuleAddSkipsMatchingExistingRule(t *testing.T) {
	runner := &recordingRunner{stdout: "9999: to 203.0.113.10 lookup main"}
	rule := planner.TunPolicyRulePlan{Priority: planner.ServerRulePriority, Selector: "to 203.0.113.10/32", Table: planner.MainRoutingTable, Action: "add"}

	step, err := (IPPolicyRuleExecutor{Runner: runner}).Add(context.Background(), rule)
	if err != nil {
		t.Fatalf("expected matching existing policy rule to be accepted: %v", err)
	}
	if step.Kind != "" {
		t.Fatalf("expected no applied step for pre-existing policy rule, got %#v", step)
	}
	want := [][]string{{"ip", "-4", "rule", "show", "priority", "9999"}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", want, runner.commands)
	}
}

func TestIPPolicyRuleAddSkipsMatchingRuleWhenPriorityHasMultipleLines(t *testing.T) {
	runner := &recordingRunner{stdout: "9999: from all lookup 51820\n9999: to 203.0.113.10 lookup main"}
	rule := planner.TunPolicyRulePlan{Priority: planner.ServerRulePriority, Selector: "to 203.0.113.10/32", Table: planner.MainRoutingTable, Action: "add"}

	step, err := (IPPolicyRuleExecutor{Runner: runner}).Add(context.Background(), rule)
	if err != nil {
		t.Fatalf("expected matching policy rule among same-priority rules to be accepted: %v", err)
	}
	if step.Kind != "" {
		t.Fatalf("expected no applied step for pre-existing matching policy rule, got %#v", step)
	}
	want := [][]string{{"ip", "-4", "rule", "show", "priority", "9999"}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", want, runner.commands)
	}
}

func TestIPPolicyRuleAddCreatesMissingRule(t *testing.T) {
	runner := &recordingRunner{results: []CommandResult{{Stdout: ""}, {}, {}}}
	rule := planner.TunPolicyRulePlan{Priority: planner.ServerRulePriority, Selector: "to 203.0.113.10/32", Table: planner.MainRoutingTable, Action: "add"}

	step, err := (IPPolicyRuleExecutor{Runner: runner}).Add(context.Background(), rule)
	if err != nil {
		t.Fatalf("expected missing policy rule to be added: %v", err)
	}
	if step.Kind != "policy-rule" || step.Owner != OwnerPolicyRule {
		t.Fatalf("expected applied policy rule step, got %#v", step)
	}
	want := [][]string{
		{"ip", "-4", "rule", "show", "priority", "9999"},
		{"ip", "-4", "rule", "add", "priority", "9999", "to", "203.0.113.10/32", "lookup", "main"},
		{"ip", "-4", "route", "flush", "cache"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", want, runner.commands)
	}
}

func TestIPPolicyRuleAddFailsForConflictingExistingRule(t *testing.T) {
	runner := &recordingRunner{stdout: "9999: from all lookup 51820"}
	rule := planner.TunPolicyRulePlan{Priority: planner.ServerRulePriority, Selector: "to 203.0.113.10/32", Table: planner.MainRoutingTable, Action: "add"}

	if _, err := (IPPolicyRuleExecutor{Runner: runner}).Add(context.Background(), rule); err == nil {
		t.Fatal("expected conflicting existing policy rule to fail")
	}
	want := [][]string{{"ip", "-4", "rule", "show", "priority", "9999"}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("unexpected commands:\nwant %#v\n got %#v", want, runner.commands)
	}
}

func TestIPPolicyRuleVerifyAcceptsMatchingRuleWhenPriorityHasMultipleLines(t *testing.T) {
	runner := &recordingRunner{stdout: "9999: from all lookup 51820\n9999: to 203.0.113.10 lookup main"}
	rule := planner.TunPolicyRulePlan{Priority: planner.ServerRulePriority, Selector: "to 203.0.113.10/32", Table: planner.MainRoutingTable, Action: "add"}

	if err := (IPPolicyRuleExecutor{Runner: runner}).Verify(context.Background(), rule); err != nil {
		t.Fatalf("expected verify to accept matching rule among same-priority rules: %v", err)
	}
}

func TestIPPolicyRuleVerifyFailsWhenSamePriorityHasNoMatchingRule(t *testing.T) {
	runner := &recordingRunner{stdout: "9999: from all lookup 51820\n9999: to 198.51.100.10 lookup main"}
	rule := planner.TunPolicyRulePlan{Priority: planner.ServerRulePriority, Selector: "to 203.0.113.10/32", Table: planner.MainRoutingTable, Action: "add"}

	if err := (IPPolicyRuleExecutor{Runner: runner}).Verify(context.Background(), rule); err == nil {
		t.Fatal("expected verify to fail when same-priority rules contain no matching planned rule")
	}
}
