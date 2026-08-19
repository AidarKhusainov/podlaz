package daemon

import (
	"context"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestBuildLifecycleDiagnosticContextIncludesExactRollbackTargets(t *testing.T) {
	runtimeDir := t.TempDir()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx := txstate.NewTransaction("tx-diagnostic", "profile-1", planner.ModeTun, time.Unix(1_700_000_000, 0).UTC())
	tx.State = txstate.TransactionCommitted
	tx.DesiredPlan.TUNAddress = txstate.TUNAddressDesiredState{
		Family:            "ipv4",
		InterfaceName:     "podlaz0",
		CIDR:              planner.DefaultTunIPv4CIDR,
		Scope:             "global",
		LinkIndex:         7,
		LinkKind:          "tun",
		AppearedAfterCore: true,
		Owner:             netexecutor.OwnerTunAddress,
	}
	tx.Rollback.TUNAddresses = []txstate.TUNAddressRollback{{
		Family:            "ipv4",
		InterfaceName:     "podlaz0",
		CIDR:              planner.DefaultTunIPv4CIDR,
		Scope:             "global",
		LinkIndex:         7,
		LinkKind:          "tun",
		AppearedAfterCore: true,
		Owner:             netexecutor.OwnerTunAddress,
	}}
	tx.DesiredPlan.Routes = []txstate.RoutePlan{{Kind: "route", Table: "51820", CIDR: "default", Dev: "podlaz0", Owner: netexecutor.OwnerRoute, Operation: "add"}}
	tx.Rollback.Routes = []txstate.RouteRollback{{Table: "51820", CIDR: "default", Dev: "podlaz0", Owner: netexecutor.OwnerRoute}}
	tx.DesiredPlan.Steps = []txstate.PlannedStep{{Kind: "policy-rule", Target: "priority 10000 from all lookup 51820", Owner: netexecutor.OwnerPolicyRule}}
	tx.Rollback.PolicyRules = []txstate.PolicyRuleRollback{{Priority: 10000, From: "all", Table: "51820", Owner: netexecutor.OwnerPolicyRule}}
	tx.AppliedSteps = []txstate.AppliedStep{
		{Kind: "tun-address", Target: "podlaz0@ifindex=7:" + planner.DefaultTunIPv4CIDR, Owner: netexecutor.OwnerTunAddress, AppliedAt: time.Now().UTC()},
		{Kind: "route", Target: "51820 default", Owner: netexecutor.OwnerRoute, AppliedAt: time.Now().UTC()},
		{Kind: "policy-rule", Target: "priority 10000 from all lookup 51820", Owner: netexecutor.OwnerPolicyRule, AppliedAt: time.Now().UTC()},
	}
	if _, err := store.Save(tx); err != nil {
		t.Fatal(err)
	}

	ctx := buildLifecycleDiagnosticContext(context.Background(), runtimeDir, "tx-diagnostic")
	if ctx.TransactionID != tx.ID || ctx.TransactionState != string(tx.State) {
		t.Fatalf("unexpected transaction context: %#v", ctx)
	}
	if ctx.RollbackAvailable != "yes" {
		t.Fatalf("expected exact rollback availability, got %#v", ctx)
	}
	if len(ctx.OwnedResources) != 3 {
		t.Fatalf("expected exact owned resource projection, got %#v", ctx.OwnedResources)
	}
}

func TestBuildLifecycleDiagnosticContextFailsClosedForAmbiguousRollback(t *testing.T) {
	runtimeDir := t.TempDir()
	store := txstate.TransactionStore{RuntimeDir: runtimeDir}
	tx := txstate.NewTransaction("tx-ambiguous", "profile-1", planner.ModeTun, time.Unix(1_700_000_000, 0).UTC())
	tx.State = txstate.TransactionCommitted
	tx.Rollback.PolicyRules = []txstate.PolicyRuleRollback{{Priority: 42, From: "all", Table: "other", Owner: netexecutor.OwnerPolicyRule}}
	if _, err := store.Save(tx); err != nil {
		t.Fatal(err)
	}

	ctx := buildLifecycleDiagnosticContext(context.Background(), runtimeDir, tx.ID)
	if ctx.RollbackAvailable != "no" {
		t.Fatalf("ambiguous rollback must not be published as available: %#v", ctx)
	}
	if len(ctx.OwnedResources) != 0 {
		t.Fatalf("ambiguous rollback must not publish cleanup targets: %#v", ctx.OwnedResources)
	}
}

func TestBuildLifecycleDiagnosticContextUsesUnknownWhenTransactionMissing(t *testing.T) {
	ctx := buildLifecycleDiagnosticContext(context.Background(), t.TempDir(), "missing")
	if ctx.TransactionState != "unknown" || ctx.RollbackAvailable != "unknown" {
		t.Fatalf("missing transaction must remain unknown, got %#v", ctx)
	}
}
