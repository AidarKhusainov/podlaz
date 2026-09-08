package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestObserveVerifiedNftTableRejectsZeroGeneration(t *testing.T) {
	plan := productionShapedPrivacyEnvelopePlanForTest()
	runner := &nftMutationObservationRunner{
		tablesJSON: nftTablesPresenceJSONForTest(plan.Family, plan.Table, 10),
		tableJSON:  canonicalPrivacyEnvelopeJSONForTest(),
	}
	backend := &nftMutationBackend{
		getGeneration: func(context.Context) (uint32, error) { return 0, nil },
	}

	_, _, err := observeVerifiedNftTableForMutation(
		context.Background(), runner, backend, plan.Family, plan.Table,
		planner.TunFirewallPlan{Family: plan.Family, Table: plan.Table, Chains: plan.Chains, Rules: plan.Rules},
	)
	if err == nil {
		t.Fatal("zero nftables generation must fail closed instead of disabling the mutation guard")
	}
	if !strings.Contains(err.Error(), "zero") {
		t.Fatalf("zero generation error=%v, want actionable zero-generation failure", err)
	}
	if runner.presenceCalls != 0 || runner.tableListCalls != 0 {
		t.Fatalf("zero generation must stop before authority-bearing observation, presence/table calls=%d/%d", runner.presenceCalls, runner.tableListCalls)
	}
}
