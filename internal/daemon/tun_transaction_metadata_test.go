package daemon

import (
	"context"
	"fmt"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTunTransactionRecordsRollbackOnlyForAppliedSteps(t *testing.T) {
	runtimeDir := t.TempDir()
	executor := &appliedOnlyTunExecutor{steps: []netexecutor.Step{
		{Kind: "route", Target: "podlaz default", Owner: netexecutor.OwnerRoute},
	}}
	result, err := runTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "test-profile"}, transactionPlanForTest(), executor, fixedClock())
	if err != nil {
		t.Fatalf("run TUN transaction failed: %v", err)
	}
	tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(result.TransactionID)
	if err != nil {
		t.Fatalf("load transaction: %v", err)
	}
	if len(tx.Rollback.Routes) != 1 || tx.Rollback.Routes[0].CIDR != "default" {
		t.Fatalf("unexpected route rollback metadata: %#v", tx.Rollback.Routes)
	}
	if len(tx.Rollback.PolicyRules) != 0 {
		t.Fatalf("unexpected policy rule rollback metadata: %#v", tx.Rollback.PolicyRules)
	}
	if len(tx.Rollback.TUN) != 0 {
		t.Fatalf("Xray-owned link must not produce TUN rollback: %#v", tx.Rollback.TUN)
	}
}

func TestDesiredPlanRecordsXrayOwnerForVerifiedTunLink(t *testing.T) {
	plan := transactionPlanForTest()
	plan.TunDevice.Action = "verify"
	desired := desiredPlanFromTunPlan(plan)
	if desired.TUN.Owner != xrayTunInboundOwner {
		t.Fatalf("expected Xray TUN owner for verified link, got %q", desired.TUN.Owner)
	}
	rollback := rollbackMetadataFromTunPlan(plan)
	if len(rollback.TUN) != 0 {
		t.Fatalf("verified Xray-owned link must not produce podlaz TUN rollback metadata: %#v", rollback.TUN)
	}
}

func TestLegacyTunAddActionStillRecordsOwnedRollbackMetadata(t *testing.T) {
	plan := transactionPlanForTest()
	plan.TunDevice.Action = "add"

	desired := desiredPlanFromTunPlan(plan)
	if desired.TUN.Owner != netexecutor.OwnerTunDevice {
		t.Fatalf("expected legacy add action to remain podlaz-owned, got %q", desired.TUN.Owner)
	}
	rollback := rollbackMetadataFromTunPlan(plan)
	if len(rollback.TUN) != 1 || rollback.TUN[0].InterfaceName != "podlaz0" || rollback.TUN[0].Owner != netexecutor.OwnerTunDevice {
		t.Fatalf("expected legacy add action TUN rollback metadata, got %#v", rollback.TUN)
	}
}

type appliedOnlyTunExecutor struct {
	steps []netexecutor.Step
}

func (e *appliedOnlyTunExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return e.steps, nil
}

func (e *appliedOnlyTunExecutor) Verify(context.Context, planner.TunPlan) error { return nil }

func (e *appliedOnlyTunExecutor) Rollback(context.Context, planner.TunPlan) error { return nil }

func TestDesiredPlanRecordsTunAddressIntentWithoutPrematureRollbackAuthority(t *testing.T) {
	plan := transactionPlanForTest()
	plan.TunDevice.Action = "verify"
	plan.TunAddress = planner.TunAddressPlan{
		Family:      "ipv4",
		Interface:   "podlaz0",
		CIDR:        planner.DefaultTunIPv4CIDR,
		Scope:       "global",
		Action:      planner.TunAddressActionAssign,
		Owner:       planner.TunAddressOwner,
		RollbackKey: "podlaz0/" + planner.DefaultTunIPv4CIDR,
		LinkKind:    "tun",
	}

	desired := desiredPlanFromTunPlan(plan)
	if desired.TUNAddress.CIDR != planner.DefaultTunIPv4CIDR || desired.TUNAddress.LinkIndex != 0 || desired.TUNAddress.Owner != netexecutor.OwnerTunAddress {
		t.Fatalf("unexpected desired address intent: %#v", desired.TUNAddress)
	}
	if rollback := rollbackMetadataFromTunPlan(plan); len(rollback.TUNAddresses) != 0 {
		t.Fatalf("unbound address intent must not grant rollback authority: %#v", rollback.TUNAddresses)
	}
}

func TestSaveTunAddressIdentityPersistsBindingBeforeMutation(t *testing.T) {
	runtimeDir := t.TempDir()
	clock := fixedClock()
	plan := transactionPlanForTest()
	plan.TunAddress = boundTunAddressPlanForMetadataTest()
	result, err := beginTunTransaction(context.Background(), runtimeDir, profile.Profile{ID: "example-profile"}, plan, clock)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	if err := saveTunAddressIdentityMetadata(result.Store, result.TransactionID, plan.TunAddress, clock()); err != nil {
		t.Fatalf("save bound address identity: %v", err)
	}
	tx, _, err := result.Store.Load(result.TransactionID)
	if err != nil {
		t.Fatalf("load transaction: %v", err)
	}
	if tx.DesiredPlan.TUNAddress.LinkIndex != 7 || tx.DesiredPlan.TUNAddress.LinkKind != "tun" {
		t.Fatalf("bound identity not persisted: %#v", tx.DesiredPlan.TUNAddress)
	}
	if len(tx.Rollback.TUNAddresses) != 0 {
		t.Fatalf("binding alone must not claim applied address cleanup: %#v", tx.Rollback.TUNAddresses)
	}
}

func TestRollbackMetadataIncludesOnlyAppliedBoundTunAddress(t *testing.T) {
	plan := transactionPlanForTest()
	plan.TunAddress = boundTunAddressPlanForMetadataTest()
	partial := rollbackPlanFromAppliedSteps(plan, []netexecutor.Step{{
		Kind:   "tun-address",
		Target: tunAddressStepTargetForMetadataTest(plan.TunAddress),
		Owner:  netexecutor.OwnerTunAddress,
	}})
	metadata := rollbackMetadataFromTunPlan(partial)
	if len(metadata.TUNAddresses) != 1 {
		t.Fatalf("expected one address rollback record, got %#v", metadata.TUNAddresses)
	}
	got := metadata.TUNAddresses[0]
	if got.InterfaceName != "podlaz0" || got.CIDR != planner.DefaultTunIPv4CIDR || got.LinkIndex != 7 || got.LinkKind != "tun" || got.Owner != netexecutor.OwnerTunAddress {
		t.Fatalf("unexpected address rollback metadata: %#v", got)
	}
}

func boundTunAddressPlanForMetadataTest() planner.TunAddressPlan {
	return planner.TunAddressPlan{
		Family:            "ipv4",
		Interface:         "podlaz0",
		CIDR:              planner.DefaultTunIPv4CIDR,
		Scope:             "global",
		Action:            planner.TunAddressActionAssign,
		Owner:             planner.TunAddressOwner,
		RollbackKey:       "podlaz0/" + planner.DefaultTunIPv4CIDR,
		LinkIndex:         7,
		LinkKind:          "tun",
		AppearedAfterCore: true,
	}
}

func tunAddressStepTargetForMetadataTest(plan planner.TunAddressPlan) string {
	return fmt.Sprintf("%s@ifindex=%d:%s", plan.Interface, plan.LinkIndex, plan.CIDR)
}
