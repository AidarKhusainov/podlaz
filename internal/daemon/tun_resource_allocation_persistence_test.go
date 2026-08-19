package daemon

import (
	"context"
	"strconv"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestBeginTunTransactionPersistsExactAllocatedResourcesBeforeApplying(t *testing.T) {
	s := netsnapshot.FakeResolvedDesktop()
	s.IPv4Routes.Routes = append(s.IPv4Routes.Routes, netsnapshot.Route{Status: netsnapshot.StatusDetected, Family: "ipv4", Destination: "default", Table: strconv.Itoa(planner.TunRoutingTableID), Interface: "test0"})
	s.IPv4PolicyRules.Rules = []netsnapshot.PolicyRoutingSignal{
		{Kind: "rule", Priority: strconv.Itoa(planner.ServerRulePriority), Selector: "to 198.51.100.10", Table: "main"},
		{Kind: "rule", Priority: strconv.Itoa(planner.TunRulePriority), Selector: "from all", Table: strconv.Itoa(planner.TunRoutingTableID)},
	}
	p := persistenceTestProfile()
	plan, err := planner.PlanTunForSession(p, s, planner.TunOptions{})
	if err != nil {
		t.Fatalf("PlanTunForSession() error = %v", err)
	}
	want, err := planner.TunResourceAllocationFromPlan(plan)
	if err != nil {
		t.Fatalf("TunResourceAllocationFromPlan() error = %v", err)
	}

	runtimeDir := t.TempDir()
	now := func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	result, err := beginTunTransaction(context.Background(), runtimeDir, p, plan, now)
	if err != nil {
		t.Fatalf("beginTunTransaction() error = %v", err)
	}
	persisted, _, err := result.Store.Load(result.TransactionID)
	if err != nil {
		t.Fatalf("load persisted transaction: %v", err)
	}
	if persisted.State != txstate.TransactionApplying {
		t.Fatalf("beginTunTransaction must persist desired allocation before applying, got state %s", persisted.State)
	}
	if len(persisted.AppliedSteps) != 0 || len(persisted.Rollback.PolicyRules) != 0 {
		t.Fatalf("planned allocation must not become premature cleanup authority: steps=%#v rollback=%#v", persisted.AppliedSteps, persisted.Rollback.PolicyRules)
	}
	got, err := persistedTunResourceAllocation(persisted)
	if err != nil {
		t.Fatalf("persistedTunResourceAllocation() error = %v", err)
	}
	if got != want {
		t.Fatalf("persisted allocation = %#v, want %#v", got, want)
	}
}

func TestPersistedTunResourceAllocationFailsClosedOnIncompletePlannedRuleIdentity(t *testing.T) {
	tx := txstate.Transaction{
		DesiredPlan: txstate.DesiredPlan{
			TUNAddress: txstate.TUNAddressDesiredState{CIDR: "198.18.0.2/32"},
			Routes:     []txstate.RoutePlan{{Kind: "route", Table: "51821", CIDR: "default", Dev: netsnapshot.DefaultTunName}},
			Steps: []txstate.PlannedStep{{
				Kind:   "policy-rule",
				Target: "priority 9998 to 203.0.113.10/32 lookup main",
				Owner:  netexecutor.OwnerPolicyRule,
			}},
		},
	}
	if _, err := persistedTunResourceAllocation(tx); err == nil {
		t.Fatal("expected incomplete planned policy-rule allocation to fail closed")
	}
}

func persistenceTestProfile() profile.Profile {
	return profile.Profile{
		ID:               "issue260-persistence",
		Name:             "Issue 260 Persistence",
		Source:           profile.SourceImportedFile,
		Engine:           profile.EngineXray,
		Server:           "vpn.example.test",
		Port:             443,
		Protocol:         "vless",
		UserIdentity:     "00000000-0000-0000-0000-000000000260",
		Transport:        "tcp",
		Security:         "reality",
		Encryption:       "none",
		Flow:             "xtls-rprx-vision",
		ServerName:       "vpn.example.test",
		Fingerprint:      "chrome",
		RealityPublicKey: "documentation-public-key",
		RealityShortID:   "abcd",
		RealitySpiderX:   "/",
	}
}
