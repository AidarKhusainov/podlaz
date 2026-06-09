package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	netexecutor "github.com/AidarKhusainov/tunwarden/internal/network/executor"
	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/tunwarden/internal/network/snapshot"
	"github.com/AidarKhusainov/tunwarden/internal/profile"
	txstate "github.com/AidarKhusainov/tunwarden/internal/state"
)

const dnsRouteOnlyDomain = "~."

type tunPlanExecutor interface {
	Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error)
	Verify(context.Context, planner.TunPlan) error
	Rollback(context.Context, planner.TunPlan) error
}

type tunTransactionResult struct {
	TransactionID   string
	TransactionPath string
	Plan            planner.TunPlan
}

func (m *XrayManager) connectTun(ctx context.Context, req api.ConnectRequest) (api.LifecycleResponse, error) {
	p := profileFromSnapshot(req.Profile)
	if err := profile.Validate(p); err != nil {
		return api.LifecycleResponse{}, err
	}

	m.mu.Lock()
	if m.cmd != nil || m.state.Connection == "active" {
		m.mu.Unlock()
		return api.LifecycleResponse{}, errors.New("connection already active; run tunwarden disconnect before connecting another profile")
	}
	m.mu.Unlock()

	runtimeDir := m.runtimeDir()
	snapshot := netsnapshot.Collect(ctx, netsnapshot.Options{Server: p.Server})
	plan, err := planner.PlanTun(p, snapshot)
	if err != nil {
		return api.LifecycleResponse{}, err
	}

	result, err := runTunTransaction(ctx, runtimeDir, p, plan, netexecutor.NewOSDNSExecutor(), time.Now)
	if err != nil {
		return api.LifecycleResponse{}, err
	}

	active := xrayState{
		Connection:    "active",
		Mode:          planner.ModeTun,
		ProfileID:     p.ID,
		ProfileName:   p.Name,
		Proxy:         "not started in this executor slice",
		TUN:           fmt.Sprintf("enabled (%s)", plan.TunDevice.Name),
		Routes:        fmt.Sprintf("applied %d route(s) and %d policy rule(s)", len(appliedRoutes(plan)), len(appliedPolicyRules(plan))),
		DNS:           dnsStatusLine(plan.DNS),
		Firewall:      firewallStatusLine(plan.Firewall),
		TransactionID: result.TransactionID,
		Warnings: append([]string{
			"TUN executor slice requires daemon CAP_NET_ADMIN privileges; nftables rollback removes only the TunWarden-owned inet tunwarden table",
		}, plan.Warnings...),
	}
	m.mu.Lock()
	m.state = active
	m.mu.Unlock()
	return lifecycleResponse(active), nil
}

func (m *XrayManager) disconnectTun(ctx context.Context, transactionID string) (api.LifecycleResponse, error) {
	store := txstate.TransactionStore{RuntimeDir: m.runtimeDir()}
	tx, _, err := store.Load(transactionID)
	if err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("load TUN transaction %s: %w", transactionID, err)
	}
	plan := tunPlanFromTransaction(tx)
	if err := rollbackTunTransaction(ctx, store, &tx, plan, netexecutor.NewOSDNSExecutor()); err != nil {
		return api.LifecycleResponse{}, err
	}
	m.mu.Lock()
	m.state = inactiveXrayState()
	m.mu.Unlock()
	return lifecycleResponse(inactiveXrayState()), nil
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
	if _, _, err := store.Transition(tx.ID, txstate.TransactionCommitted); err != nil {
		partialPlan := rollbackPlanFromAppliedSteps(plan, steps)
		return tunTransactionResult{}, rollbackTunFailure(ctx, store, &tx, partialPlan, executor, steps, err)
	}
	return tunTransactionResult{TransactionID: tx.ID, TransactionPath: path, Plan: plan}, nil
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
	return fmt.Errorf("%w; rolled back applied TunWarden-owned TUN, route, policy-rule, DNS, and nftables state", cause)
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
	if err := executor.Rollback(ctx, plan); err != nil {
		return err
	}
	if _, err := txstate.Transition(tx, txstate.TransactionRolledBack, transactionNow(store)); err != nil {
		return err
	}
	_, err := store.Save(*tx)
	return err
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

func tunPlanFromTransaction(tx txstate.Transaction) planner.TunPlan {
	plan := planner.TunPlan{Mode: tx.Mode, ProfileID: tx.ProfileID}
	if len(tx.Rollback.TUN) > 0 {
		plan.TunDevice = planner.TunDevicePlan{Name: tx.Rollback.TUN[0].InterfaceName, MTU: tx.DesiredPlan.TUN.MTU, Action: "add"}
	}
	for _, route := range tx.Rollback.Routes {
		plan.Routes = append(plan.Routes, planner.TunRoutePlan{Family: "ipv4", Destination: route.CIDR, Table: route.Table, Gateway: route.Via, Interface: route.Dev, Action: "add"})
	}
	for _, rule := range tx.Rollback.PolicyRules {
		selector := strings.TrimSpace(rule.From)
		if rule.To != "" {
			selector = "to " + rule.To
		} else if selector != "" && !strings.HasPrefix(selector, "from ") {
			selector = "from " + selector
		}
		plan.PolicyRules = append(plan.PolicyRules, planner.TunPolicyRulePlan{Family: "ipv4", Priority: rule.Priority, Selector: selector, Table: rule.Table, Action: "add"})
	}
	if len(tx.Rollback.DNS) > 0 {
		dns := tx.Rollback.DNS[0]
		plan.DNS = planner.TunDNSPlan{Backend: dns.Backend, TargetLink: dns.Link, Servers: append([]string{}, tx.DesiredPlan.DNS.Servers...), Action: planner.DNSActionConfigure}
	} else if tx.DesiredPlan.DNS.Link != "" {
		plan.DNS = planner.TunDNSPlan{Backend: tx.DesiredPlan.DNS.Backend, TargetLink: tx.DesiredPlan.DNS.Link, Servers: append([]string{}, tx.DesiredPlan.DNS.Servers...), Action: planner.DNSActionConfigure}
	}
	if len(tx.Rollback.NFTables) > 0 {
		nft := tx.Rollback.NFTables[0]
		plan.Firewall = planner.TunFirewallPlan{Backend: planner.FirewallBackendNftables, Family: nft.Family, Table: nft.Table, TableAction: planner.FirewallTableAction}
	} else if tx.DesiredPlan.NFT.Table != "" {
		plan.Firewall = planner.TunFirewallPlan{Backend: planner.FirewallBackendNftables, Family: tx.DesiredPlan.NFT.Family, Table: tx.DesiredPlan.NFT.Table, TableAction: planner.FirewallTableAction}
	}
	return plan
}

func routeTarget(route planner.TunRoutePlan) string {
	return route.Table + " " + route.Destination
}

func policyRuleTarget(rule planner.TunPolicyRulePlan) string {
	return fmt.Sprintf("priority %d %s lookup %s", rule.Priority, rule.Selector, rule.Table)
}

func appliedRoutes(plan planner.TunPlan) []planner.TunRoutePlan {
	out := make([]planner.TunRoutePlan, 0, len(plan.Routes))
	for _, route := range plan.Routes {
		if route.Action == "add" {
			out = append(out, route)
		}
	}
	return out
}

func appliedPolicyRules(plan planner.TunPlan) []planner.TunPolicyRulePlan {
	out := make([]planner.TunPolicyRulePlan, 0, len(plan.PolicyRules))
	for _, rule := range plan.PolicyRules {
		if rule.Action == "add" {
			out = append(out, rule)
		}
	}
	return out
}

func dnsStatusLine(plan planner.TunDNSPlan) string {
	if plan.Action == planner.DNSActionConfigure && plan.TargetLink != "" {
		return fmt.Sprintf("%s; Link: %s; Servers: %s; Rollback: available", plan.Backend, plan.TargetLink, strings.Join(plan.Servers, ", "))
	}
	if plan.Action == planner.DNSActionBlocked {
		return "blocked: " + plan.Reason
	}
	return "not modified"
}

func firewallStatusLine(plan planner.TunFirewallPlan) string {
	if plan.TableAction == planner.FirewallTableAction && plan.Table != "" {
		return fmt.Sprintf("%s; Table: %s %s; Kill-switch: %s; Rollback: %s", plan.Backend, plan.Family, plan.Table, plan.KillSwitch.Policy, plan.Rollback)
	}
	if plan.TableAction == planner.FirewallActionBlocked {
		return "blocked: " + plan.Reason
	}
	if plan.TableAction == planner.FirewallActionValidate {
		return fmt.Sprintf("%s; Table: %s %s requires ownership validation before apply", plan.Backend, plan.Family, plan.Table)
	}
	return "not modified"
}

func nftChains(plan planner.TunFirewallPlan) []txstate.NFTChainPlan {
	chains := make([]txstate.NFTChainPlan, 0, len(plan.Chains))
	rules := firewallRuleStrings(plan.Rules)
	for _, chain := range plan.Chains {
		chains = append(chains, txstate.NFTChainPlan{
			Name:     chain.Name,
			Hook:     chain.Hook,
			Type:     chain.Type,
			Priority: chain.Priority,
			Policy:   chain.Policy,
			Rules:    append([]string{}, rules...),
			Owner:    netexecutor.OwnerFirewall,
		})
	}
	return chains
}

func firewallRuleStrings(rules []planner.TunFirewallRulePlan) []string {
	out := make([]string, 0, len(rules))
	for _, rule := range rules {
		if rule.Action != planner.FirewallActionAdd {
			continue
		}
		out = append(out, strings.TrimSpace(rule.Expr+" "+rule.Verdict+" owner "+rule.Ownership))
	}
	return out
}

func firewallTarget(plan planner.TunFirewallPlan) string {
	return strings.TrimSpace(plan.Family + " " + plan.Table)
}

func dnsSearchDomains(plan planner.TunDNSPlan) []string {
	if plan.Action != planner.DNSActionConfigure || plan.TargetLink == "" {
		return nil
	}
	return []string{dnsRouteOnlyDomain}
}

func transactionNow(store txstate.TransactionStore) time.Time {
	if store.Now != nil {
		return store.Now().UTC()
	}
	return time.Now().UTC()
}
