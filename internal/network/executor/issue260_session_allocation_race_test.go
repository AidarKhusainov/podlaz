package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestExclusivePolicyRuleAddRejectsMatchingRuleThatAppearedAfterAllocation(t *testing.T) {
	runner := &recordingRunner{stdout: "98: from all to 203.0.113.10 lookup main\n"}
	exec := IPPolicyRuleExecutor{Runner: runner}
	plan := planner.TunPolicyRulePlan{
		Family:   "ipv4",
		Priority: 98,
		Selector: "to 203.0.113.10",
		Table:    planner.MainRoutingTable,
		Action:   planner.TunActionAddExclusive,
	}

	step, err := exec.Add(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "became occupied before apply") {
		t.Fatalf("expected raced allocation collision, got step=%#v err=%v", step, err)
	}
	if step != (Step{}) {
		t.Fatalf("foreign raced rule must not produce ownership proof: %#v", step)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("exclusive collision must only inspect, not add/delete: %#v", runner.commands)
	}
}

func TestLegacyPolicyRuleAddRemainsIdempotentForExactRecoveryPlan(t *testing.T) {
	runner := &recordingRunner{stdout: "98: from all to 203.0.113.10 lookup main\n"}
	exec := IPPolicyRuleExecutor{Runner: runner}
	plan := planner.TunPolicyRulePlan{
		Family:   "ipv4",
		Priority: 98,
		Selector: "to 203.0.113.10",
		Table:    planner.MainRoutingTable,
		Action:   "add",
	}

	step, err := exec.Add(context.Background(), plan)
	if err != nil {
		t.Fatalf("legacy/recovery idempotent add error = %v", err)
	}
	if step != (Step{}) {
		t.Fatalf("pre-existing exact recovery rule must not claim a new applied step: %#v", step)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("idempotent recovery should only inspect existing exact rule: %#v", runner.commands)
	}
}
