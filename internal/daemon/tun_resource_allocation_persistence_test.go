package daemon

import (
	"strconv"
	"testing"
	"time"

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

	store := txstate.TransactionStore{RuntimeDir: t.TempDir(), Now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }}
	tx, err := beginTunTransaction(store, p, plan, "/run/podlaz/generated/xray.json", store.Now())
	if err != nil {
		t.Fatalf("beginTunTransaction() error = %v", err)
	}
	if tx.State != txstate.TransactionPlanned {
		t.Fatalf("transaction must be durably planned before applying, got %s", tx.State)
	}

	persisted, _, err := store.Load(tx.ID)
	if err != nil {
		t.Fatalf("load persisted transaction: %v", err)
	}
	got, err := persistedTunResourceAllocation(persisted)
	if err != nil {
		t.Fatalf("persistedTunResourceAllocation() error = %v", err)
	}
	if got != want {
		t.Fatalf("persisted allocation = %#v, want %#v", got, want)
	}
}

func TestPersistedTunResourceAllocationFailsClosedOnIncompleteRuleOwnership(t *testing.T) {
	tx := txstate.Transaction{
		DesiredPlan: txstate.DesiredPlan{
			TUNAddress: txstate.TUNAddressDesiredState{CIDR: "198.18.0.2/32"},
			Routes: []txstate.RoutePlan{{Kind: "route", Table: "51821", CIDR: "default", Dev: netsnapshot.DefaultTunName}},
		},
		Rollback: txstate.RollbackMetadata{
			PolicyRules: []txstate.PolicyRuleRollback{{Priority: 9998, To: "203.0.113.10/32", Table: "main", Owner: "podlaz:policy-rule"}},
		},
	}
	if _, err := persistedTunResourceAllocation(tx); err == nil {
		t.Fatal("expected incomplete exact policy-rule ownership to fail closed")
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
