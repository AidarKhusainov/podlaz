package executor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestExclusivePolicyRuleDetectsSamePriorityRaceAndRollsBackOnlyExactRule(t *testing.T) {
	runner := &policyRulePriorityRaceRunner{}
	exec := IPPolicyRuleExecutor{Runner: runner}
	plan := planner.TunPolicyRulePlan{
		Family:   "ipv4",
		Priority: 10001,
		Selector: planner.IPv4DefaultSelector,
		Table:    "51821",
		Action:   planner.TunActionAddExclusive,
		Reason:   "synthetic allocated session rule",
	}

	step, err := exec.Add(context.Background(), plan)
	if err == nil {
		t.Fatalf("expected post-add priority-bucket collision, got step=%#v", step)
	}
	if step.Kind != "policy-rule" || step.Owner != OwnerPolicyRule {
		t.Fatalf("post-mutation priority collision must retain exact owned rollback step, got %#v err=%v", step, err)
	}
	if !runner.ownRule || !runner.foreignRule {
		t.Fatalf("race fixture did not create both rules: own=%v foreign=%v", runner.ownRule, runner.foreignRule)
	}

	if err := exec.Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback exact owned policy rule: %v", err)
	}
	if runner.ownRule {
		t.Fatal("rollback left the Podlaz-owned policy rule behind")
	}
	if !runner.foreignRule {
		t.Fatal("rollback removed the foreign same-priority policy rule")
	}
}

func TestPolicyRuleRollbackRefusesIndistinguishableDuplicateTuple(t *testing.T) {
	runner := &duplicatePolicyRuleRollbackRunner{copies: 2}
	exec := IPPolicyRuleExecutor{Runner: runner}
	plan := planner.TunPolicyRulePlan{
		Family:   "ipv4",
		Priority: 10001,
		Selector: planner.IPv4DefaultSelector,
		Table:    "51821",
		Action:   planner.TunActionAddExclusive,
	}

	if err := exec.Rollback(context.Background(), plan); err == nil {
		t.Fatal("rollback must fail closed when two indistinguishable exact rules share the priority")
	}
	if runner.deleteCalls != 0 || runner.copies != 2 {
		t.Fatalf("ambiguous rollback mutated the priority bucket: deletes=%d copies=%d", runner.deleteCalls, runner.copies)
	}
}

type policyRulePriorityRaceRunner struct {
	ownRule     bool
	foreignRule bool
}

func (r *policyRulePriorityRaceRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	switch command {
	case "ip -4 rule show priority 10001":
		var lines []string
		if r.ownRule {
			lines = append(lines, "10001: from all lookup 51821")
		}
		if r.foreignRule {
			lines = append(lines, "10001: from 198.51.100.0/24 lookup 60000")
		}
		return CommandResult{Stdout: strings.Join(lines, "\n")}, nil
	case "ip -4 rule add priority 10001 from all lookup 51821":
		r.ownRule = true
		// Foreign state appears after the empty-bucket check and before the
		// session can prove that the priority remains a unique ordering identity.
		r.foreignRule = true
		return CommandResult{}, nil
	case "ip -4 rule del priority 10001 from all lookup 51821":
		r.ownRule = false
		return CommandResult{}, nil
	case "ip -4 route flush cache":
		return CommandResult{}, nil
	default:
		return CommandResult{ExitCode: 127, Stderr: "unexpected command"}, fmt.Errorf("unexpected command: %s", command)
	}
}

type duplicatePolicyRuleRollbackRunner struct {
	copies      int
	deleteCalls int
}

func (r *duplicatePolicyRuleRollbackRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	switch command {
	case "ip -4 rule show priority 10001":
		lines := make([]string, r.copies)
		for i := range lines {
			lines[i] = "10001: from all lookup 51821"
		}
		return CommandResult{Stdout: strings.Join(lines, "\n")}, nil
	case "ip -4 rule del priority 10001 from all lookup 51821":
		r.deleteCalls++
		if r.copies > 0 {
			r.copies--
		}
		return CommandResult{}, nil
	case "ip -4 route flush cache":
		return CommandResult{}, nil
	default:
		return CommandResult{ExitCode: 127, Stderr: "unexpected command"}, fmt.Errorf("unexpected command: %s", command)
	}
}
