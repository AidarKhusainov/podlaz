package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

type tunPlanExecutor interface {
	Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error)
	Verify(context.Context, planner.TunPlan) error
	Rollback(context.Context, planner.TunPlan) error
}

type tunTransactionResult struct {
	TransactionID   string
	TransactionPath string
	Plan            planner.TunPlan
	Store           txstate.TransactionStore
}

func runTunTransaction(ctx context.Context, runtimeDir string, p profile.Profile, plan planner.TunPlan, executor tunPlanExecutor, now func() time.Time) (tunTransactionResult, error) {
	if executor == nil {
		return tunTransactionResult{}, errors.New("missing TUN executor")
	}
	if now == nil {
		now = time.Now
	}
	store := txstate.TransactionStore{RuntimeDir: runtimeDir, Now: now}
	tx := txstate.NewTransaction(newTunTransactionID(now), p.ID, planner.ModeTun, now())
	tx.BeforeSnapshot = snapshotMetadata(plan.Snapshot, now())
	tx.DesiredPlan = desiredPlanFromTunPlan(plan)
	tx.Rollback = rollbackMetadataFromTunPlan(plan)
	path, err := store.Save(tx)
	if err != nil {
		return tunTransactionResult{}, err
	}

	if _, _, err := store.Transition(tx.ID, txstate.TransactionApplying); err != nil {
		return tunTransactionResult{}, err
	}
	tx, _, err = store.Load(tx.ID)
	if err != nil {
		return tunTransactionResult{}, err
	}
	steps, err := executor.Apply(ctx, plan)
	if err != nil {
		partialPlan := rollbackPlanFromAppliedSteps(plan, steps)
		return tunTransactionResult{}, rollbackTunFailure(ctx, store, &tx, partialPlan, executor, steps, fmt.Errorf("apply TUN plan: %w", err))
	}
	tx.AppliedSteps = appliedStepsFromExecutor(steps, now())
	if _, err := store.Save(tx); err != nil {
		partialPlan := rollbackPlanFromAppliedSteps(plan, steps)
		return tunTransactionResult{}, rollbackTunFailure(ctx, store, &tx, partialPlan, executor, steps, fmt.Errorf("record applied TUN plan: %w", err))
	}
	if _, _, err := store.Transition(tx.ID, txstate.TransactionApplied); err != nil {
		partialPlan := rollbackPlanFromAppliedSteps(plan, steps)
		return tunTransactionResult{}, rollbackTunFailure(ctx, store, &tx, partialPlan, executor, steps, err)
	}
	if _, _, err := store.Transition(tx.ID, txstate.TransactionVerifying); err != nil {
		partialPlan := rollbackPlanFromAppliedSteps(plan, steps)
		return tunTransactionResult{}, rollbackTunFailure(ctx, store, &tx, partialPlan, executor, steps, err)
	}
	tx, _, err = store.Load(tx.ID)
	if err != nil {
		return tunTransactionResult{}, err
	}
	if err := executor.Verify(ctx, plan); err != nil {
		partialPlan := rollbackPlanFromAppliedSteps(plan, steps)
		return tunTransactionResult{}, rollbackTunFailure(ctx, store, &tx, partialPlan, executor, steps, fmt.Errorf("verify TUN plan: %w", err))
	}
	return tunTransactionResult{TransactionID: tx.ID, TransactionPath: path, Plan: plan, Store: store}, nil
}

func commitTunTransaction(store txstate.TransactionStore, transactionID string) error {
	if _, _, err := store.Transition(transactionID, txstate.TransactionCommitted); err != nil {
		return fmt.Errorf("commit TUN transaction %s: %w", transactionID, err)
	}
	return nil
}

func saveCoreRollbackMetadata(store txstate.TransactionStore, transactionID, runtimeConfigPath string, pid int, now time.Time) error {
	tx, _, err := store.Load(transactionID)
	if err != nil {
		return fmt.Errorf("load TUN transaction %s: %w", transactionID, err)
	}
	tx.DesiredPlan.Core = txstate.CorePlan{
		RuntimeConfigPath: runtimeConfigPath,
		ProcessLabel:      "xray",
		Owner:             txstate.TransactionOwner,
	}
	tx.Rollback.GeneratedConfigs = []txstate.GeneratedConfigRollback{{Path: runtimeConfigPath, Owner: txstate.TransactionOwner}}
	if pid > 0 {
		tx.Rollback.ChildProcesses = []txstate.ChildProcessRollback{{PID: pid, Label: "xray", ConfigRef: runtimeConfigPath, Owner: txstate.TransactionOwner}}
	}
	tx.Health = txstate.HealthResult{Status: "core-started", CheckedAt: now.UTC(), Message: "Xray process stayed alive during startup verification"}
	_, err = store.Save(tx)
	return err
}

func rollbackTunFailure(ctx context.Context, store txstate.TransactionStore, tx *txstate.Transaction, rollbackPlan planner.TunPlan, executor tunPlanExecutor, steps []netexecutor.Step, cause error) error {
	tx.AppliedSteps = appliedStepsFromExecutor(steps, transactionNow(store))
	tx.Rollback = rollbackMetadataFromTunPlan(rollbackPlan)
	_, _ = store.Save(*tx)
	if err := rollbackTunTransaction(ctx, store, tx, rollbackPlan, executor); err != nil {
		_, _ = txstate.MarkFailure(tx, err.Error(), transactionNow(store))
		_, _ = store.Save(*tx)
		return errors.Join(cause, fmt.Errorf("rollback TUN plan: %w", err))
	}
	return fmt.Errorf("%w; rolled back applied podlaz-owned TUN, route, policy-rule, DNS, and nftables state", cause)
}

func rollbackTunTransaction(ctx context.Context, store txstate.TransactionStore, tx *txstate.Transaction, plan planner.TunPlan, executor tunPlanExecutor) error {
	if executor == nil {
		return errors.New("missing TUN executor")
	}
	if tx.State == txstate.TransactionRolledBack {
		return nil
	}
	if tx.State != txstate.TransactionRollingBack {
		if _, err := txstate.Transition(tx, txstate.TransactionRollingBack, transactionNow(store)); err != nil {
			return err
		}
		if _, err := store.Save(*tx); err != nil {
			return err
		}
	}

	var rollbackErrs []error
	if err := stopRollbackChildProcesses(*tx); err != nil {
		rollbackErrs = append(rollbackErrs, err)
	}
	for _, cfg := range tx.Rollback.GeneratedConfigs {
		removeGeneratedConfig(cfg.Path)
	}
	if err := executor.Rollback(ctx, plan); err != nil {
		rollbackErrs = append(rollbackErrs, err)
	}
	if len(rollbackErrs) > 0 {
		return errors.Join(rollbackErrs...)
	}
	if _, err := txstate.Transition(tx, txstate.TransactionRolledBack, transactionNow(store)); err != nil {
		return err
	}
	_, err := store.Save(*tx)
	return err
}

func removeTransactionFile(store txstate.TransactionStore, transactionID string) error {
	path, err := store.Path(transactionID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func rollbackPlanFromAppliedSteps(plan planner.TunPlan, steps []netexecutor.Step) planner.TunPlan {
	rollback := planner.TunPlan{Mode: plan.Mode, TunnelMode: plan.TunnelMode, ProfileID: plan.ProfileID, ProfileName: plan.ProfileName}
	for _, step := range steps {
		switch step.Kind {
		case "tun-device":
			if step.Target == plan.TunDevice.Name {
				rollback.TunDevice = plan.TunDevice
			}
		case "route":
			for _, route := range plan.Routes {
				if routeTarget(route) == step.Target {
					rollback.Routes = append(rollback.Routes, route)
				}
			}
		case "policy-rule":
			for _, rule := range plan.PolicyRules {
				if policyRuleTarget(rule) == step.Target {
					rollback.PolicyRules = append(rollback.PolicyRules, rule)
				}
			}
		case "dns":
			if step.Target == plan.DNS.TargetLink {
				rollback.DNS = plan.DNS
			}
		case "nftables":
			if step.Target == firewallTarget(plan.Firewall) {
				rollback.Firewall = plan.Firewall
			}
		}
	}
	return rollback
}

func newTunTransactionID(now func() time.Time) string {
	return "tun-" + now().UTC().Format("20060102T150405.000000000Z")
}

func snapshotMetadata(s netsnapshot.Snapshot, now time.Time) txstate.SnapshotMetadata {
	summary := []string{
		"default IPv4 route: " + string(s.DefaultIPv4.Status),
		"server route: " + string(s.ServerRoute.Status),
		fmt.Sprintf("tun devices inspected: %d", len(s.TunDevices)),
	}
	return txstate.SnapshotMetadata{CapturedAt: now.UTC(), Source: "daemon network snapshot", Summary: summary}
}

func desiredPlanFromTunPlan(plan planner.TunPlan) txstate.DesiredPlan {
	routes := make([]txstate.RoutePlan, 0, len(plan.Routes))
	steps := make([]txstate.PlannedStep, 0, len(plan.Steps)+len(plan.PolicyRules)+len(plan.Firewall.Rules)+2)
	for _, route := range plan.Routes {
		if route.Action != "add" {
			continue
		}
		routes = append(routes, txstate.RoutePlan{
			Kind:      "route",
			Table:     route.Table,
			CIDR:      route.Destination,
			Via:       route.Gateway,
			Dev:       route.Interface,
			Owner:     netexecutor.OwnerRoute,
			Operation: route.Action,
		})
	}
	for _, step := range plan.Steps {
		steps = append(steps, txstate.PlannedStep{Kind: "plan", Target: planner.ModeTun, Description: step, Owner: txstate.TransactionOwner})
	}
	for _, rule := range plan.PolicyRules {
		steps = append(steps, txstate.PlannedStep{Kind: "policy-rule", Target: policyRuleTarget(rule), Description: rule.Reason, Owner: netexecutor.OwnerPolicyRule})
	}
	if plan.DNS.Action == planner.DNSActionConfigure && plan.DNS.TargetLink != "" {
		steps = append(steps, txstate.PlannedStep{Kind: "dns", Target: plan.DNS.TargetLink, Description: plan.DNS.Reason, Owner: netexecutor.OwnerDNS})
	}
	if plan.Firewall.TableAction == planner.FirewallTableAction && plan.Firewall.Table != "" {
		steps = append(steps, txstate.PlannedStep{Kind: "nftables", Target: firewallTarget(plan.Firewall), Description: plan.Firewall.Reason, Owner: netexecutor.OwnerFirewall})
	}
	return txstate.DesiredPlan{
		PlanID: plan.ProfileID + ":" + planner.ModeTun,
		TUN: txstate.TUNDesiredState{
			InterfaceName: plan.TunDevice.Name,
			MTU:           plan.TunDevice.MTU,
			Owner:         netexecutor.OwnerTunDevice,
		},
		Routes: routes,
		DNS: txstate.DNSPlan{
			Backend:       plan.DNS.Backend,
			Link:          plan.DNS.TargetLink,
			Servers:       append([]string{}, plan.DNS.Servers...),
			SearchDomains: dnsSearchDomains(plan.DNS),
			Owner:         txstate.TransactionOwner,
		},
		NFT: txstate.NFTPlan{
			Family: plan.Firewall.Family,
			Table:  plan.Firewall.Table,
			Chains: nftChains(plan.Firewall),
			Owner:  netexecutor.OwnerFirewall,
		},
		Steps: steps,
	}
}

func rollbackMetadataFromTunPlan(plan planner.TunPlan) txstate.RollbackMetadata {
	routes := make([]txstate.RouteRollback, 0, len(plan.Routes))
	for _, route := range plan.Routes {
		if route.Action != "add" {
			continue
		}
		routes = append(routes, txstate.RouteRollback{Table: route.Table, CIDR: route.Destination, Via: route.Gateway, Dev: route.Interface, Owner: netexecutor.OwnerRoute})
	}
	rules := make([]txstate.PolicyRuleRollback, 0, len(plan.PolicyRules))
	for _, rule := range plan.PolicyRules {
		if rule.Action != "add" {
			continue
		}
		rules = append(rules, policyRuleRollback(rule))
	}
	metadata := txstate.RollbackMetadata{Routes: routes, PolicyRules: rules}
	if plan.TunDevice.Name != "" {
		metadata.TUN = []txstate.TUNRollback{{InterfaceName: plan.TunDevice.Name, Owner: netexecutor.OwnerTunDevice}}
	}
	if plan.DNS.Action == planner.DNSActionConfigure && plan.DNS.TargetLink != "" {
		metadata.DNS = []txstate.DNSRollback{{Backend: plan.DNS.Backend, Link: plan.DNS.TargetLink, SearchDomains: dnsSearchDomains(plan.DNS), Owner: netexecutor.OwnerDNS}}
	}
	if plan.Firewall.TableAction == planner.FirewallTableAction && plan.Firewall.Table != "" {
		metadata.NFTables = []txstate.NFTablesRollback{{Family: plan.Firewall.Family, Table: plan.Firewall.Table, Owner: netexecutor.OwnerFirewall}}
	}
	return metadata
}

func policyRuleRollback(rule planner.TunPolicyRulePlan) txstate.PolicyRuleRollback {
	rollback := txstate.PolicyRuleRollback{Priority: rule.Priority, Table: rule.Table, Owner: netexecutor.OwnerPolicyRule}
	selector := strings.TrimSpace(rule.Selector)
	switch {
	case strings.HasPrefix(selector, "to "):
		rollback.To = strings.TrimSpace(strings.TrimPrefix(selector, "to "))
	case strings.HasPrefix(selector, "from "):
		rollback.From = strings.TrimSpace(strings.TrimPrefix(selector, "from "))
	default:
		rollback.From = selector
	}
	return rollback
}

func appliedStepsFromExecutor(steps []netexecutor.Step, now time.Time) []txstate.AppliedStep {
	out := make([]txstate.AppliedStep, 0, len(steps))
	for _, step := range steps {
		out = append(out, txstate.AppliedStep{Kind: step.Kind, Target: step.Target, Description: step.Description, Owner: step.Owner, AppliedAt: now.UTC()})
	}
	return out
}
