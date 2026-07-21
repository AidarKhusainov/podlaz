package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

const (
	OwnerDNS                = "podlaz:dns-link"
	resolvedRouteOnlyDomain = "~."

	defaultResolvedApplyAttempts      = 20
	defaultResolvedApplyPollInterval  = 100 * time.Millisecond
	defaultResolvedVerifyAttempts     = 20
	defaultResolvedVerifyPollInterval = 100 * time.Millisecond
)

// DNSExecutor owns systemd-resolved per-link DNS apply, verification, and cleanup.
type DNSExecutor interface {
	Apply(context.Context, planner.TunDNSPlan) (Step, error)
	Verify(context.Context, planner.TunDNSPlan) error
	Rollback(context.Context, planner.TunDNSPlan) error
}

// DNSAwareTunExecutor composes the existing TUN/route executor with DNS and
// optional firewall apply without changing the low-level route executor contract.
// DNS and firewall are applied only from already-inspected desired state and are
// rolled back before the TUN link is deleted.
type DNSAwareTunExecutor struct {
	Base     TunExecutor
	DNS      DNSExecutor
	Firewall FirewallExecutor
}

// NewOSDNSExecutor returns the canonical Linux iproute2 + systemd-resolved +
// nftables executor composition.
func NewOSDNSExecutor() DNSAwareTunExecutor {
	return newDNSExecutorWithRunner(OSRunner{})
}

// Apply applies TUN, routes, policy rules, systemd-resolved per-link DNS, and
// podlaz-owned nftables state from the already-inspected plan.
func (e DNSAwareTunExecutor) Apply(ctx context.Context, plan planner.TunPlan) ([]Step, error) {
	if err := e.validate(plan); err != nil {
		return nil, err
	}
	steps, err := e.Base.Apply(ctx, plan)
	if err != nil {
		return steps, err
	}
	if shouldApplyDNS(plan.DNS) {
		dnsStep, err := e.DNS.Apply(ctx, plan.DNS)
		if err != nil {
			if rollbackErr := e.DNS.Rollback(ctx, plan.DNS); rollbackErr != nil {
				return steps, errors.Join(err, fmt.Errorf("rollback DNS after failed apply: %w", rollbackErr))
			}
			return steps, err
		}
		steps = append(steps, dnsStep)
	}
	if shouldApplyFirewall(plan.Firewall) {
		firewallStep, err := e.Firewall.Apply(ctx, plan.Firewall)
		if err != nil {
			if rollbackErr := e.Firewall.Rollback(ctx, plan.Firewall); rollbackErr != nil {
				return steps, errors.Join(err, fmt.Errorf("rollback nftables after failed apply: %w", rollbackErr))
			}
			return steps, err
		}
		steps = append(steps, firewallStep)
	}
	return steps, nil
}

// Verify checks base TUN state, systemd-resolved per-link DNS state, and
// podlaz-owned nftables state.
func (e DNSAwareTunExecutor) Verify(ctx context.Context, plan planner.TunPlan) error {
	if err := e.validate(plan); err != nil {
		return err
	}
	if err := e.Base.Verify(ctx, plan); err != nil {
		return err
	}
	if shouldApplyDNS(plan.DNS) {
		if err := e.DNS.Verify(ctx, plan.DNS); err != nil {
			return err
		}
	}
	if shouldApplyFirewall(plan.Firewall) {
		return e.Firewall.Verify(ctx, plan.Firewall)
	}
	return nil
}

// Rollback reverts firewall first, then DNS, routes, policy rules, and the TUN link.
func (e DNSAwareTunExecutor) Rollback(ctx context.Context, plan planner.TunPlan) error {
	var errs []error
	if e.Firewall != nil && strings.TrimSpace(plan.Firewall.Table) != "" {
		if err := e.Firewall.Rollback(ctx, plan.Firewall); err != nil {
			errs = append(errs, err)
		}
	}
	if e.DNS != nil && strings.TrimSpace(plan.DNS.TargetLink) != "" {
		if err := e.DNS.Rollback(ctx, plan.DNS); err != nil {
			errs = append(errs, err)
		}
	}
	if err := e.Base.Rollback(ctx, plan); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (e DNSAwareTunExecutor) validate(plan planner.TunPlan) error {
	if e.DNS == nil && shouldApplyDNS(plan.DNS) {
		return errors.New("missing DNS executor")
	}
	if e.Firewall == nil && shouldApplyFirewall(plan.Firewall) {
		return errors.New("missing firewall executor")
	}
	return nil
}

func shouldApplyDNS(plan planner.TunDNSPlan) bool {
	return strings.TrimSpace(plan.TargetLink) != "" || len(plan.Servers) > 0 || strings.TrimSpace(plan.Action) != ""
}

func shouldApplyFirewall(plan planner.TunFirewallPlan) bool {
	return strings.TrimSpace(plan.Table) != "" || strings.TrimSpace(plan.Action) != ""
}

// ResolvedDNSExecutor owns the systemd-resolved per-link configuration.
type ResolvedDNSExecutor struct {
	Runner             CommandRunner
	ApplyAttempts      int
	ApplyPollInterval  time.Duration
	VerifyAttempts     int
	VerifyPollInterval time.Duration
	Sleep              func(context.Context, time.Duration) error
}

func (e ResolvedDNSExecutor) Apply(ctx context.Context, plan planner.TunDNSPlan) (Step, error) {
	if err := validateResolvedDNSPlan(plan); err != nil {
		return Step{}, err
	}
	if err := e.run(ctx, "resolvectl", "revert", plan.TargetLink); err != nil && !resolvedResourceMissing(err) {
		return Step{}, fmt.Errorf("reset stale DNS link state for %s: %w", plan.TargetLink, err)
	}
	serverArgs := append([]string{"dns", plan.TargetLink}, plan.Servers...)
	if err := e.runApplyCommand(ctx, plan.TargetLink, serverArgs...); err != nil {
		return Step{}, fmt.Errorf("configure DNS servers for %s: %w", plan.TargetLink, err)
	}
	if err := e.runApplyCommand(ctx, plan.TargetLink, "domain", plan.TargetLink, resolvedRouteOnlyDomain); err != nil {
		return Step{}, fmt.Errorf("configure route-only DNS domain for %s: %w", plan.TargetLink, err)
	}
	if err := e.runApplyCommand(ctx, plan.TargetLink, "default-route", plan.TargetLink, "yes"); err != nil {
		return Step{}, fmt.Errorf("configure DNS default route for %s: %w", plan.TargetLink, err)
	}
	return Step{Kind: "dns", Target: plan.TargetLink, Description: plan.Reason, Owner: OwnerDNS}, nil
}

func (e ResolvedDNSExecutor) Verify(ctx context.Context, plan planner.TunDNSPlan) error {
	if err := validateResolvedDNSPlan(plan); err != nil {
		return err
	}
	attempts := e.verifyAttempts()
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		result, err := observeCommand(ctx, e.Runner, "resolvectl", "status", "--no-pager")
		if err == nil {
			lastErr = verifyResolvedDNSStatus(plan, result.Stdout)
			if lastErr == nil {
				return nil
			}
		} else {
			lastErr = err
		}
		if attempt+1 < attempts {
			if err := e.sleep(ctx, e.verifyPollInterval()); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("verify systemd-resolved DNS for %s: %w", plan.TargetLink, lastErr)
}

func (e ResolvedDNSExecutor) Rollback(ctx context.Context, plan planner.TunDNSPlan) error {
	if strings.TrimSpace(plan.TargetLink) == "" {
		return nil
	}
	if err := e.run(ctx, "resolvectl", "revert", plan.TargetLink); err != nil && !resolvedResourceMissing(err) {
		return fmt.Errorf("revert systemd-resolved DNS for %s: %w", plan.TargetLink, err)
	}
	return nil
}

func (e ResolvedDNSExecutor) run(ctx context.Context, name string, args ...string) error {
	return runCommand(ctx, e.Runner, name, args...)
}

func (e ResolvedDNSExecutor) runApplyCommand(ctx context.Context, targetLink string, args ...string) error {
	attempts := e.applyAttempts()
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		lastErr = e.run(ctx, "resolvectl", args...)
		if lastErr == nil {
			return nil
		}
		if !resolvedResourceMissing(lastErr) {
			return lastErr
		}
		if attempt+1 < attempts {
			if err := e.sleep(ctx, e.applyPollInterval()); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("systemd-resolved did not register link %s within retry window: %w", targetLink, lastErr)
}

func (e ResolvedDNSExecutor) applyAttempts() int {
	if e.ApplyAttempts > 0 {
		return e.ApplyAttempts
	}
	return defaultResolvedApplyAttempts
}

func (e ResolvedDNSExecutor) applyPollInterval() time.Duration {
	if e.ApplyPollInterval > 0 {
		return e.ApplyPollInterval
	}
	return defaultResolvedApplyPollInterval
}

func (e ResolvedDNSExecutor) verifyAttempts() int {
	if e.VerifyAttempts > 0 {
		return e.VerifyAttempts
	}
	return defaultResolvedVerifyAttempts
}

func (e ResolvedDNSExecutor) verifyPollInterval() time.Duration {
	if e.VerifyPollInterval > 0 {
		return e.VerifyPollInterval
	}
	return defaultResolvedVerifyPollInterval
}

func (e ResolvedDNSExecutor) sleep(ctx context.Context, duration time.Duration) error {
	if e.Sleep != nil {
		return e.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func validateResolvedDNSPlan(plan planner.TunDNSPlan) error {
	if strings.TrimSpace(plan.TargetLink) == "" {
		return errors.New("missing DNS target link")
	}
	if plan.Action == planner.DNSActionBlocked {
		return fmt.Errorf("DNS plan is blocked: %s", plan.Reason)
	}
	if plan.Backend != planner.DNSBackendSystemdResolved {
		return fmt.Errorf("unsupported DNS backend %q", plan.Backend)
	}
	if len(plan.Servers) == 0 {
		return errors.New("missing planned DNS servers")
	}
	return nil
}

func resolvedResourceMissing(err error) bool {
	return commandErrorContains(err, "link "+netsnapshot.DefaultTunName+" does not exist", `failed to resolve interface "`+netsnapshot.DefaultTunName+`": no such device`)
}

func verifyResolvedDNSStatus(plan planner.TunDNSPlan, output string) error {
	links := netsnapshot.ParseResolvedLinks(output)
	targetLinks := resolvedLinksByName(links, plan.TargetLink)
	if len(targetLinks) == 0 {
		return fmt.Errorf("link status not found for %s", plan.TargetLink)
	}
	for _, link := range targetLinks {
		if resolvedLinkMatchesPlan(link, plan) {
			if foreign := foreignResolvedRouteOnlyOwner(links, plan.TargetLink); foreign != "" {
				return fmt.Errorf("foreign route-only DNS owner remains on %s", foreign)
			}
			return nil
		}
	}
	return resolvedDNSMismatch(plan, targetLinks[0])
}

func resolvedLinkMatchesPlan(link netsnapshot.ResolvedLink, plan planner.TunDNSPlan) bool {
	if !containsString(link.Domains, resolvedRouteOnlyDomain) || !link.DefaultRoute {
		return false
	}
	for _, server := range plan.Servers {
		if !containsString(link.DNSServers, server) {
			return false
		}
	}
	return true
}

func resolvedDNSMismatch(plan planner.TunDNSPlan, link netsnapshot.ResolvedLink) error {
	for _, server := range plan.Servers {
		if !containsString(link.DNSServers, server) {
			return fmt.Errorf("DNS server %s not found on link %s", server, plan.TargetLink)
		}
	}
	if !containsString(link.Domains, resolvedRouteOnlyDomain) {
		return fmt.Errorf("route-only domain %s not found on link %s", resolvedRouteOnlyDomain, plan.TargetLink)
	}
	if !link.DefaultRoute {
		return fmt.Errorf("DNS default route is not enabled on link %s", plan.TargetLink)
	}
	return fmt.Errorf("systemd-resolved link %s does not match planned configuration", plan.TargetLink)
}

func resolvedLinksByName(links []netsnapshot.ResolvedLink, name string) []netsnapshot.ResolvedLink {
	var matches []netsnapshot.ResolvedLink
	for _, link := range links {
		if link.Name == name {
			matches = append(matches, link)
		}
	}
	return matches
}

func foreignResolvedRouteOnlyOwner(links []netsnapshot.ResolvedLink, targetName string) string {
	for _, link := range links {
		if link.Name == targetName {
			continue
		}
		if containsString(link.Domains, resolvedRouteOnlyDomain) && link.CurrentScopesDNS {
			return link.Name
		}
	}
	return ""
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
