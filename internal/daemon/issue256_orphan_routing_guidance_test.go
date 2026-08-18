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
	rule := txstate.PolicyRuleRollback{
		Priority: planner.TunRulePriority,
		From:     "all",
		Table:    planner.TunRoutingTable,
		Owner:    netexecutor.OwnerPolicyRule,
	}
	writeIssue256ValidatedPolicyRuleTransaction(t, runtimeDir, "partial-routing", []txstate.PolicyRuleRollback{rule})
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
	writeIssue256ValidatedPolicyRuleTransaction(t, runtimeDir, "exact-routing", []txstate.PolicyRuleRollback{
		{Priority: planner.ServerRulePriority, To: "203.0.113.10/32", Table: planner.MainRoutingTable, Owner: netexecutor.OwnerPolicyRule},
		{Priority: planner.TunRulePriority, From: "all", Table: planner.TunRoutingTable, Owner: netexecutor.OwnerPolicyRule},
	})
	withIssue256OrphanPolicyRules(t)

	manager := &XrayManager{RuntimeDir: runtimeDir}
	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
	if err == nil {
		t.Fatal("expected transaction-backed stale preflight blocker")
	}
	assertIssue256RecoveryGuidance(t, err.Error())
}

func TestIssue256ExactRouteRollbackKeepsRecoveryGuidance(t *testing.T) {
	runtimeDir := t.TempDir()
	route := txstate.RouteRollback{Table: planner.TunRoutingTable, CIDR: planner.IPv4DefaultRoute, Dev: netsnapshot.DefaultTunName, Owner: netexecutor.OwnerRoute}
	writeIssue256ValidatedRouteTransaction(t, runtimeDir, "exact-route", route)
	withIssue256ObservedRoute(t, "default dev podlaz0")

	manager := &XrayManager{RuntimeDir: runtimeDir}
	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
	if err == nil {
		t.Fatal("expected exact route-backed stale preflight blocker")
	}
	assertIssue256RecoveryGuidance(t, err.Error())
}

func TestIssue256DifferentRouteRollbackDoesNotAuthorizeObservedRoute(t *testing.T) {
	runtimeDir := t.TempDir()
	route := txstate.RouteRollback{Table: planner.TunRoutingTable, CIDR: "198.51.100.0/24", Dev: netsnapshot.DefaultTunName, Owner: netexecutor.OwnerRoute}
	writeIssue256ValidatedRouteTransaction(t, runtimeDir, "different-route", route)
	withIssue256ObservedRoute(t, "default dev podlaz0")

	manager := &XrayManager{RuntimeDir: runtimeDir}
	_, err := manager.prepareTunHandoff(context.Background(), netsnapshot.FakeResolvedDesktop(), api.HandoffBlock, netsnapshot.Options{})
	if err == nil {
		t.Fatal("expected unmatched observed route to keep preflight fail-closed")
	}
	assertIssue256ManualRoutingGuidance(t, err.Error())
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

func withIssue256ObservedRoute(t *testing.T, raw string) {
	t.Helper()
	original := podlazRuntimeRoutingStaleResources
	podlazRuntimeRoutingStaleResources = func(context.Context) []netsnapshot.StaleResource {
		return []netsnapshot.StaleResource{{Kind: "route", Name: netsnapshot.DefaultRouteTableID, Status: netsnapshot.StatusDetected, Detail: raw}}
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

func writeIssue256ValidatedPolicyRuleTransaction(t *testing.T, runtimeDir, id string, rules []txstate.PolicyRuleRollback) {
	t.Helper()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tx := txstate.NewTransaction(id, "test-profile", planner.ModeTun, now)
	tx.State = txstate.TransactionFailed
	tx.FailureReason = "synthetic safe failure"
	tx.Rollback.PolicyRules = append([]txstate.PolicyRuleRollback(nil), rules...)
	for _, rule := range rules {
		target := issue256PolicyRuleTarget(rule)
		tx.DesiredPlan.Steps = append(tx.DesiredPlan.Steps, txstate.PlannedStep{Kind: "policy-rule", Target: target, Owner: netexecutor.OwnerPolicyRule})
		tx.AppliedSteps = append(tx.AppliedSteps, txstate.AppliedStep{Kind: "policy-rule", Target: target, Owner: netexecutor.OwnerPolicyRule, AppliedAt: now})
	}
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}
}

func writeIssue256ValidatedRouteTransaction(t *testing.T, runtimeDir, id string, route txstate.RouteRollback) {
	t.Helper()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tx := txstate.NewTransaction(id, "test-profile", planner.ModeTun, now)
	tx.State = txstate.TransactionFailed
	tx.FailureReason = "synthetic safe failure"
	tx.Rollback.Routes = []txstate.RouteRollback{route}
	tx.DesiredPlan.Routes = []txstate.RoutePlan{{Kind: "route", Table: route.Table, CIDR: route.CIDR, Via: route.Via, Dev: route.Dev, Owner: netexecutor.OwnerRoute, Operation: "add"}}
	tx.AppliedSteps = []txstate.AppliedStep{{Kind: "route", Target: strings.TrimSpace(route.Table) + " " + strings.TrimSpace(route.CIDR), Owner: netexecutor.OwnerRoute, AppliedAt: now}}
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}
}

func issue256PolicyRuleTarget(rule txstate.PolicyRuleRollback) string {
	selector := "from " + strings.TrimSpace(rule.From)
	if to := strings.TrimSpace(rule.To); to != "" {
		selector = "to " + to
	}
	return "priority " + issue256Int(rule.Priority) + " " + selector + " lookup " + strings.TrimSpace(rule.Table)
}

func issue256Int(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var out [20]byte
	i := len(out)
	for value > 0 {
		i--
		out[i] = digits[value%10]
		value /= 10
	}
	return string(out[i:])
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

func assertIssue256RecoveryGuidance(t *testing.T, message string) {
	t.Helper()
	if !strings.Contains(message, "plz recover --execute --yes") {
		t.Fatalf("exact transaction-backed routing must retain authoritative recovery guidance: %s", message)
	}
	if strings.Contains(message, "ownership evidence is unavailable") || strings.Contains(message, "Remove them manually") {
		t.Fatalf("exact transaction-backed routing must not be classified as orphan ownership: %s", message)
	}
}
