package executor

import (
	"context"
	"errors"
	"reflect"
	"syscall"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestObserveVerifiedNftTableRetriesOnlyReadWhenGenerationChanges(t *testing.T) {
	plan := productionShapedPrivacyEnvelopePlanForTest()
	runner := &nftMutationObservationRunner{
		tablesJSON: nftTablesPresenceJSONForTest(plan.Family, plan.Table, 10),
		tableJSON:  canonicalPrivacyEnvelopeJSONForTest(),
	}
	generations := []uint32{40, 41, 42, 42}
	generationCalls := 0
	backend := &nftMutationBackend{
		getGeneration: func(context.Context) (uint32, error) {
			if generationCalls >= len(generations) {
				t.Fatalf("unexpected generation read %d", generationCalls)
			}
			value := generations[generationCalls]
			generationCalls++
			return value, nil
		},
	}

	target, absent, err := observeVerifiedNftTableForMutation(
		context.Background(), runner, backend, plan.Family, plan.Table,
		planner.TunFirewallPlan{Family: plan.Family, Table: plan.Table, Chains: plan.Chains, Rules: plan.Rules},
	)
	if err != nil {
		t.Fatalf("coherent observation: %v", err)
	}
	if absent {
		t.Fatal("exact table unexpectedly classified absent")
	}
	if target.Generation != 42 || target.Handle != 10 || target.Family != plan.Family || target.Table != plan.Table {
		t.Fatalf("unexpected mutation target: %#v", target)
	}
	if generationCalls != 4 {
		t.Fatalf("generation calls=%d, want 4 for one discarded and one coherent observation", generationCalls)
	}
	if runner.tableListCalls != 2 {
		t.Fatalf("table list calls=%d, want 2", runner.tableListCalls)
	}
}

func TestPrivacyEnvelopeRemoveDoesNotReplayStaleMutation(t *testing.T) {
	plan := productionShapedPrivacyEnvelopePlanForTest()
	runner := &nftMutationObservationRunner{
		tablesJSON: nftTablesPresenceJSONForTest(plan.Family, plan.Table, 10),
		tableJSON:  canonicalPrivacyEnvelopeJSONForTest(),
	}
	removeCalls := 0
	backend := coherentTestMutationBackend(77)
	backend.removeTable = func(_ context.Context, target nftMutationTarget) error {
		removeCalls++
		if target.Generation != 77 || target.Handle != 10 {
			t.Fatalf("remove target=%#v", target)
		}
		return syscall.ERESTART
	}

	err := (PrivacyEnvelopeExecutor{Runner: runner, mutation: backend}).Remove(context.Background(), plan)
	if !errors.Is(err, syscall.ERESTART) {
		t.Fatalf("stale mutation error=%v, want ERESTART", err)
	}
	if removeCalls != 1 {
		t.Fatalf("stale mutation must never be replayed, remove calls=%d", removeCalls)
	}
}

func TestPrivacyEnvelopeReplaceUsesOneGenerationGuardedMutation(t *testing.T) {
	oldPlan := productionShapedPrivacyEnvelopePlanForTest()
	newPlan := oldPlan
	newPlan.Rules = append([]planner.TunFirewallRulePlan(nil), oldPlan.Rules...)
	newPlan.Rules[2].Expr = "ip daddr 198.51.100.20"

	runner := &nftMutationObservationRunner{
		tablesJSON: nftTablesPresenceJSONForTest(oldPlan.Family, oldPlan.Table, 10),
		tableJSON:  canonicalPrivacyEnvelopeJSONForTest(),
	}
	backend := coherentTestMutationBackend(88)
	replaceCalls := 0
	backend.replaceTable = func(_ context.Context, target nftMutationTarget, got planner.TunFirewallPlan) error {
		replaceCalls++
		if target.Generation != 88 || target.Handle != 10 {
			t.Fatalf("replace target=%#v", target)
		}
		if got.Family != newPlan.Family || got.Table != newPlan.Table || !reflect.DeepEqual(got.Chains, newPlan.Chains) || !reflect.DeepEqual(got.Rules, newPlan.Rules) {
			t.Fatalf("replacement plan=%#v", got)
		}
		return nil
	}

	if err := (PrivacyEnvelopeExecutor{Runner: runner, mutation: backend}).Replace(context.Background(), oldPlan, newPlan); err != nil {
		t.Fatalf("guarded replace: %v", err)
	}
	if replaceCalls != 1 {
		t.Fatalf("replace calls=%d, want one atomic mutation", replaceCalls)
	}
	if runner.mutationCommandSeen {
		t.Fatal("generation-guarded replacement must not fall back to separate nft -f mutation")
	}
}

func TestPrivacyEnvelopeReplaceDoesNotDeleteSameNameReplacementOnStaleGeneration(t *testing.T) {
	oldPlan := productionShapedPrivacyEnvelopePlanForTest()
	newPlan := oldPlan
	newPlan.Rules = append([]planner.TunFirewallRulePlan(nil), oldPlan.Rules...)
	newPlan.Rules[2].Expr = "ip daddr 198.51.100.20"

	runner := &nftMutationObservationRunner{
		tablesJSON: nftTablesPresenceJSONForTest(oldPlan.Family, oldPlan.Table, 10),
		tableJSON:  canonicalPrivacyEnvelopeJSONForTest(),
	}
	backend := coherentTestMutationBackend(99)
	replaceCalls := 0
	backend.replaceTable = func(context.Context, nftMutationTarget, planner.TunFirewallPlan) error {
		replaceCalls++
		return syscall.ERESTART
	}

	err := (PrivacyEnvelopeExecutor{Runner: runner, mutation: backend}).Replace(context.Background(), oldPlan, newPlan)
	if !errors.Is(err, syscall.ERESTART) {
		t.Fatalf("replace stale error=%v, want ERESTART", err)
	}
	if replaceCalls != 1 {
		t.Fatalf("stale replacement batch must not be replayed, calls=%d", replaceCalls)
	}
}

func coherentTestMutationBackend(generation uint32) *nftMutationBackend {
	return &nftMutationBackend{
		getGeneration: func(context.Context) (uint32, error) { return generation, nil },
	}
}

type nftMutationObservationRunner struct {
	tablesJSON          string
	tableJSON           string
	tableListCalls      int
	mutationCommandSeen bool
}

func (r *nftMutationObservationRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	if name != "nft" {
		return CommandResult{ExitCode: 1}, errors.New("unexpected command")
	}
	if reflect.DeepEqual(args, []string{"-j", "list", "tables"}) {
		return CommandResult{Stdout: r.tablesJSON}, nil
	}
	if len(args) == 5 && reflect.DeepEqual(args[:4], []string{"-j", "list", "table", "inet"}) {
		r.tableListCalls++
		return CommandResult{Stdout: r.tableJSON}, nil
	}
	r.mutationCommandSeen = true
	return CommandResult{ExitCode: 1}, errors.New("unexpected mutation command")
}

func nftTablesPresenceJSONForTest(family, table string, handle uint64) string {
	return `{"nftables":[{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},{"table":{"family":"` + family + `","name":"` + table + `","handle":10}}]}`
}
