package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestNftablesRollbackRequiresStructuredAbsenceAfterGuardedRemoval(t *testing.T) {
	plan := planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      "inet",
		Table:       "podlaz",
		TableAction: planner.FirewallTableAction,
		Chains: []planner.TunFirewallChainPlan{{
			Name:     "output",
			Type:     planner.FirewallChainTypeFilter,
			Hook:     planner.FirewallOutputHook,
			Priority: planner.FirewallOutputPriority,
			Policy:   planner.FirewallDefaultChainPolicy,
			Action:   planner.FirewallTableAction,
		}},
		Rules: []planner.TunFirewallRulePlan{{
			Chain:       "output",
			Expr:        `oifname "lo"`,
			Verdict:     planner.FirewallVerdictAccept,
			Action:      planner.FirewallActionAdd,
			Ownership:   "podlaz:firewall:loopback",
			RollbackKey: "inet/podlaz/output/loopback",
		}},
	}
	runner := &nftMutationObservationRunner{
		tablesJSON: nftTablesPresenceJSONForTest("inet", "podlaz", 10),
		tableJSON:  nftablesRollbackPostconditionTableJSON(),
	}
	removeCalls := 0
	backend := coherentTestMutationBackend(7)
	backend.removeTable = func(context.Context, nftMutationTarget) error {
		removeCalls++
		// Simulate a same-name object being present by the time the postcondition
		// is observed. The guarded delete itself succeeded, but durable cleanup
		// must not be considered complete without structured absence evidence.
		return nil
	}

	err := (NftablesExecutor{Runner: runner, mutation: backend}).Rollback(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "still present") {
		t.Fatalf("rollback without proven post-delete absence error=%v, want present postcondition failure", err)
	}
	if removeCalls != 1 {
		t.Fatalf("guarded removals=%d, want 1", removeCalls)
	}
	if runner.presenceCalls != 2 {
		t.Fatalf("presence observations=%d, want initial verification plus post-delete proof", runner.presenceCalls)
	}
}

func TestNftablesRollbackSucceedsOnlyAfterStructuredAbsence(t *testing.T) {
	plan := planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      "inet",
		Table:       "podlaz",
		TableAction: planner.FirewallTableAction,
		Chains: []planner.TunFirewallChainPlan{{
			Name:     "output",
			Type:     planner.FirewallChainTypeFilter,
			Hook:     planner.FirewallOutputHook,
			Priority: planner.FirewallOutputPriority,
			Policy:   planner.FirewallDefaultChainPolicy,
			Action:   planner.FirewallTableAction,
		}},
		Rules: []planner.TunFirewallRulePlan{{
			Chain:       "output",
			Expr:        `oifname "lo"`,
			Verdict:     planner.FirewallVerdictAccept,
			Action:      planner.FirewallActionAdd,
			Ownership:   "podlaz:firewall:loopback",
			RollbackKey: "inet/podlaz/output/loopback",
		}},
	}
	runner := &nftMutationObservationRunner{
		tablesJSON: nftTablesPresenceJSONForTest("inet", "podlaz", 10),
		tableJSON:  nftablesRollbackPostconditionTableJSON(),
	}
	backend := coherentTestMutationBackend(7)
	backend.removeTable = func(context.Context, nftMutationTarget) error {
		runner.tablesJSON = nftTablesAbsenceJSONForTest()
		return nil
	}

	if err := (NftablesExecutor{Runner: runner, mutation: backend}).Rollback(context.Background(), plan); err != nil {
		t.Fatalf("rollback with proven structured absence: %v", err)
	}
	if runner.presenceCalls != 2 {
		t.Fatalf("presence observations=%d, want initial verification plus post-delete proof", runner.presenceCalls)
	}
}

func nftablesRollbackPostconditionTableJSON() string {
	return `{"nftables":[
{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},
{"table":{"family":"inet","name":"podlaz","handle":10}},
{"chain":{"family":"inet","table":"podlaz","name":"output","handle":1,"type":"filter","hook":"output","prio":0,"policy":"accept"}},
{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":1,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"lo"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:firewall:loopback"}}
]}`
}
