package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestIssue256OrphanPolicyRoutingDoesNotPromiseUnauthoritativeRecovery(t *testing.T) {
	err := preflightTunOwnership(netsnapshot.Snapshot{StaleResources: []netsnapshot.StaleResource{
		{Kind: "policy-rule", Name: "priority-9999", Status: netsnapshot.StatusDetected},
		{Kind: "policy-rule", Name: "priority-10000", Status: netsnapshot.StatusDetected},
	}}, "block")
	if err == nil {
		t.Fatal("expected orphan routing preflight blocker")
	}
	assertIssue256ManualRoutingGuidance(t, err.Error())
}

func TestIssue256RecoverableTransactionKeepsCanonicalRecoveryGuidance(t *testing.T) {
	err := preflightTunOwnership(netsnapshot.Snapshot{StaleResources: []netsnapshot.StaleResource{
		{Kind: "transaction-file", Name: "tx-recoverable.json", Status: netsnapshot.StatusDetected, Detail: "state=committed requires daemon-owned recovery"},
	}}, "block")
	if err == nil {
		t.Fatal("expected recoverable transaction preflight blocker")
	}
	if !strings.Contains(err.Error(), "plz recover --execute --yes") {
		t.Fatalf("recoverable transaction should retain canonical recovery guidance: %s", err)
	}
}

func TestIssue256PlannedTransactionWithoutRoutingRollbackDoesNotAuthorizeOrphanRules(t *testing.T) {
	runtimeDir := t.TempDir()
	writeIssue256Transaction(t, runtimeDir, "planned-no-routing", txstate.TransactionPlanned, txstate.RollbackMetadata{})
	withIssue256OrphanPolicyRules(t)

	manager := &XrayManager{RuntimeDir: runtimeDir}
	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
	if err == nil {
		t.Fatal("expected orphan routing blocker with planned transaction")
	}
	assertIssue256ManualRoutingGuidance(t, err.Error())
}

func TestIssue256NonRoutingRollbackDoesNotAuthorizeOrphanRules(t *testing.T) {
	runtimeDir := t.TempDir()
	writeIssue256Transaction(t, runtimeDir, "generated-only", txstate.TransactionFailed, txstate.RollbackMetadata{
		GeneratedConfigs: []txstate.GeneratedConfigRollback{{
			Path:  filepath.Join(runtimeDir, "generated", "xray.json"),
			Owner: txstate.TransactionOwner,
		}},
	})
	withIssue256OrphanPolicyRules(t)

	manager := &XrayManager{RuntimeDir: runtimeDir}
	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
	if err == nil {
		t.Fatal("expected orphan routing blocker with non-routing rollback")
	}
	assertIssue256ManualRoutingGuidance(t, err.Error())
}

func TestIssue256PartialRoutingRollbackDoesNotAuthorizeUnmatchedOrphanRule(t *testing.T) {
	runtimeDir := t.TempDir()
	writeIssue256Transaction(t, runtimeDir, "partial-routing", txstate.TransactionFailed, txstate.RollbackMetadata{
		PolicyRules: []txstate.PolicyRuleRollback{{
			Priority: planner.TunRulePriority,
			From:     "all",
			Table:    planner.TunRoutingTable,
			Owner:    netexecutor.OwnerPolicyRule,
		}},
	})
	withIssue256OrphanPolicyRules(t)

	manager := &XrayManager{RuntimeDir: runtimeDir}
	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
	if err == nil {
		t.Fatal("expected unmatched orphan rule to keep preflight fail-closed")
	}
	assertIssue256ManualRoutingGuidance(t, err.Error())
}

func TestIssue256ExactRoutingRollbackKeepsRecoveryGuidance(t *testing.T) {
	runtimeDir := t.TempDir()
	writeIssue256Transaction(t, runtimeDir, "exact-routing", txstate.TransactionFailed, txstate.RollbackMetadata{
		PolicyRules: []txstate.PolicyRuleRollback{
			{Priority: planner.ServerRulePriority, To: "203.0.113.10/32", Table: planner.MainRoutingTable, Owner: netexecutor.OwnerPolicyRule},
			{Priority: planner.TunRulePriority, From: "all", Table: planner.TunRoutingTable, Owner: netexecutor.OwnerPolicyRule},
		},
	})
	withIssue256OrphanPolicyRules(t)

	manager := &XrayManager{RuntimeDir: runtimeDir}
	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
	if err == nil {
		t.Fatal("expected transaction-backed stale preflight blocker")
	}
	message := err.Error()
	if !strings.Contains(message, "plz recover --execute --yes") {
		t.Fatalf("exact transaction-backed routing must retain authoritative recovery guidance: %s", message)
	}
	if strings.Contains(message, "ownership evidence is unavailable") || strings.Contains(message, "Remove them manually") {
		t.Fatalf("exact transaction-backed routing must not be classified as orphan ownership: %s", message)
	}
}

func TestIssue256InvalidTransactionDoesNotGrantRoutingRecoveryAuthority(t *testing.T) {
	err := preflightTunOwnership(netsnapshot.Snapshot{StaleResources: []netsnapshot.StaleResource{
		{Kind: "transaction-file", Name: "invalid-or-unreadable", Status: netsnapshot.StatusDetected},
		{Kind: "route", Name: "51820", Status: netsnapshot.StatusDetected},
		{Kind: "policy-rule", Name: "10000", Status: netsnapshot.StatusDetected},
	}}, "block")
	if err == nil {
		t.Fatal("expected invalid-transaction routing preflight blocker")
	}
	assertIssue256ManualRoutingGuidance(t, err.Error())
}

func withIssue256OrphanPolicyRules(t *testing.T) {
	t.Helper()
	original := podlazRuntimeRoutingStaleResources
	podlazRuntimeRoutingStaleResources = func(context.Context) []netsnapshot.StaleResource {
		return []netsnapshot.StaleResource{
			{Kind: "policy-rule", Name: "9999", Status: netsnapshot.StatusDetected, Detail: "9999: from all to 203.0.113.10 lookup main"},
			{Kind: "policy-rule", Name: "10000", Status: netsnapshot.StatusDetected, Detail: "10000: from all lookup 51820"},
		}
	}
	t.Cleanup(func() { podlazRuntimeRoutingStaleResources = original })
}

func writeIssue256Transaction(t *testing.T, runtimeDir, id string, state txstate.TransactionState, rollback txstate.RollbackMetadata) {
	t.Helper()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tx := txstate.NewTransaction(id, "test-profile", planner.ModeTun, now)
	tx.State = state
	tx.Rollback = rollback
	if state == txstate.TransactionFailed {
		tx.FailureReason = "synthetic safe failure"
	}
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}
}

func assertIssue256ManualRoutingGuidance(t *testing.T, message string) {
	t.Helper()
	if strings.Contains(message, "recover --execute") {
		t.Fatalf("routing without exact rollback ownership must not promise recovery: %s", message)
	}
	if !strings.Contains(message, "ownership evidence is unavailable") {
		t.Fatalf("expected explicit ownership-evidence guidance, got: %s", message)
	}
	if !strings.Contains(message, "blocks TUN connect before network mutation") {
		t.Fatalf("expected fail-closed preflight wording, got: %s", message)
	}
}
