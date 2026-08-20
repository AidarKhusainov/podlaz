package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestIssue256HistoricalRoutingShapeWithoutTransactionNeverGetsRecoveryAuthority(t *testing.T) {
	resources := []netsnapshot.StaleResource{
		{Kind: "policy-rule", Name: "9999", Status: netsnapshot.StatusDetected, Detail: "9999: from all to 203.0.113.10 lookup main"},
		{Kind: "policy-rule", Name: "10000", Status: netsnapshot.StatusDetected, Detail: "10000: from all lookup 51820"},
		{Kind: "route", Name: "51820", Status: netsnapshot.StatusDetected, Detail: "default dev podlaz0 table 51820"},
	}

	markRoutingRecoveryAuthority(resources, nil)

	for _, resource := range resources {
		if resource.RecoveryAuthorized {
			t.Fatalf("historical routing resemblance granted cleanup authority: %#v", resource)
		}
	}
}

func TestIssue256NonRoutingTransactionDoesNotAuthorizeRouting(t *testing.T) {
	tx := txstate.NewTransaction("generated-only", "test-profile", planner.ModeTun, time.Unix(1_700_000_000, 0).UTC())
	tx.State = txstate.TransactionFailed
	tx.FailureReason = "synthetic safe failure"
	tx.Rollback.GeneratedConfigs = []txstate.GeneratedConfigRollback{{Path: "/run/podlaz/generated/example.json", Owner: txstate.TransactionOwner}}
	resources := []netsnapshot.StaleResource{{
		Kind: "policy-rule", Name: "10000", Status: netsnapshot.StatusDetected, Detail: "10000: from all lookup 51820",
	}}

	markRoutingRecoveryAuthority(resources, []txstate.Transaction{tx})

	if resources[0].RecoveryAuthorized {
		t.Fatalf("non-routing rollback granted routing cleanup authority: %#v", resources[0])
	}
}

func TestIssue256ExactPolicyRuleTransactionAuthorizesOnlyExactObservedTuple(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	tx := txstate.NewTransaction("exact-rule", "test-profile", planner.ModeTun, now)
	tx.State = txstate.TransactionFailed
	tx.FailureReason = "synthetic safe failure"
	rule := txstate.PolicyRuleRollback{
		Priority: planner.TunRulePriority,
		From:     "all",
		Table:    planner.TunRoutingTable,
		Owner:    netexecutor.OwnerPolicyRule,
	}
	target := issue256PolicyRuleTarget(rule)
	tx.Rollback.PolicyRules = []txstate.PolicyRuleRollback{rule}
	tx.DesiredPlan.Steps = []txstate.PlannedStep{{Kind: "policy-rule", Target: target, Owner: netexecutor.OwnerPolicyRule}}
	tx.AppliedSteps = []txstate.AppliedStep{{Kind: "policy-rule", Target: target, Owner: netexecutor.OwnerPolicyRule, AppliedAt: now}}

	resources := []netsnapshot.StaleResource{
		{Kind: "policy-rule", Name: "10000", Status: netsnapshot.StatusDetected, Detail: "10000: from all lookup 51820"},
		{Kind: "policy-rule", Name: "9999", Status: netsnapshot.StatusDetected, Detail: "9999: from all to 203.0.113.10 lookup main"},
	}
	markRoutingRecoveryAuthority(resources, []txstate.Transaction{tx})

	if !resources[0].RecoveryAuthorized {
		t.Fatalf("exact transaction-backed policy rule was not authorized: %#v", resources[0])
	}
	if resources[1].RecoveryAuthorized {
		t.Fatalf("unmatched historical policy rule was authorized: %#v", resources[1])
	}
}

func TestIssue256ExactRouteTransactionAuthorizesOnlyExactObservedTuple(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	tx := txstate.NewTransaction("exact-route", "test-profile", planner.ModeTun, now)
	tx.State = txstate.TransactionFailed
	tx.FailureReason = "synthetic safe failure"
	route := txstate.RouteRollback{
		Table: planner.TunRoutingTable,
		CIDR:  planner.IPv4DefaultRoute,
		Dev:   netsnapshot.DefaultTunName,
		Owner: netexecutor.OwnerRoute,
	}
	tx.Rollback.Routes = []txstate.RouteRollback{route}
	tx.DesiredPlan.Routes = []txstate.RoutePlan{{
		Kind: "route", Table: route.Table, CIDR: route.CIDR, Dev: route.Dev, Owner: netexecutor.OwnerRoute, Operation: "add",
	}}
	tx.AppliedSteps = []txstate.AppliedStep{{
		Kind: "route", Target: strings.TrimSpace(route.Table) + " " + strings.TrimSpace(route.CIDR), Owner: netexecutor.OwnerRoute, AppliedAt: now,
	}}

	resources := []netsnapshot.StaleResource{
		{Kind: "route", Name: "51820", Status: netsnapshot.StatusDetected, Detail: "default dev podlaz0 table 51820"},
		{Kind: "route", Name: "51820", Status: netsnapshot.StatusDetected, Detail: "198.51.100.0/24 dev podlaz0 table 51820"},
	}
	markRoutingRecoveryAuthority(resources, []txstate.Transaction{tx})

	if !resources[0].RecoveryAuthorized {
		t.Fatalf("exact transaction-backed route was not authorized: %#v", resources[0])
	}
	if resources[1].RecoveryAuthorized {
		t.Fatalf("different route in historical table was authorized: %#v", resources[1])
	}
}

func TestIssue256HistoricalRoutingShapeDoesNotBlockNewCoexistenceSession(t *testing.T) {
	s := netsnapshot.FakeResolvedDesktop()
	s.PolicyRouting = []netsnapshot.PolicyRoutingSignal{
		{Kind: "rule", Priority: "9999", Selector: "to 203.0.113.10", Table: "main", Raw: "9999: from all to 203.0.113.10 lookup main"},
		{Kind: "rule", Priority: "10000", Selector: "from all", Table: "51820", Raw: "10000: from all lookup 51820"},
	}
	s.StaleResources = []netsnapshot.StaleResource{
		{Kind: "policy-rule", Name: "9999", Status: netsnapshot.StatusDetected},
		{Kind: "policy-rule", Name: "10000", Status: netsnapshot.StatusDetected},
	}

	m := NewXrayManager(t.TempDir())
	if _, err := m.prepareTunCoexistence(context.Background(), s, "block", netsnapshot.Options{}); err != nil {
		t.Fatalf("historical routing shape without exact transaction state is baseline, not a connect blocker: %v", err)
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
