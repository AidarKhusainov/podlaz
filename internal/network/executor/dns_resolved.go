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
	if e.DNS == nil {
		return errors.New("missing DNS executor")
	}
	if err := validateDNSPlan(plan.DNS); err != nil {
		return err
	}
	if hasFirewallPlan(plan.Firewall) {
		if e.Firewall == nil {
			return errors.New("missing firewall executor")
		}
		if err := validateFirewallPlan(plan.Firewall); err != nil {
			return err
		}
	}
	return e.Base.validate()
}

// ResolvedDNSExecutor applies per-link DNS through resolvectl only. It never
// edits /etc/resolv.conf.
type ResolvedDNSExecutor struct {
	Runner CommandRunner

	// ApplyAttempts and ApplyPollInterval bound retries while systemd-resolved
	// registers a newly-created podlaz0 link. Only missing-link errors are retried.
	ApplyAttempts     int
	ApplyPollInterval time.Duration

	// VerifyAttempts and VerifyPollInterval bound systemd-resolved propagation
	// polling after Apply. Zero values use conservative production defaults.
	VerifyAttempts     int
	VerifyPollInterval time.Duration

	// Sleep is injectable for deterministic tests.
	Sleep func(context.Context, time.Duration) error
}

// Apply first removes any stale podlaz-owned per-link record, then configures
// the DNS servers, route-only default DNS domain, and per-link DNS default route.
func (e ResolvedDNSExecutor) Apply(ctx context.Context, plan planner.TunDNSPlan) (Step, error) {
	if err := validateDNSPlan(plan); err != nil {
		return Step{}, err
	}
	link := strings.TrimSpace(plan.TargetLink)
	result, err := observeCommand(ctx, e.Runner, "resolvectl", "revert", link)
	if err != nil && !resolvedCommandResultIsMissing(ctx, result, err) {
		return Step{}, fmt.Errorf("refresh stale systemd-resolved DNS for %s: %w", link, err)
	}
	args := append([]string{"dns", link}, plan.Servers...)
	if err := e.runResolvedApplyCommand(ctx, args...); err != nil {
		return Step{}, fmt.Errorf("configure systemd-resolved DNS server for %s: %w", link, err)
	}
	if err := e.runResolvedApplyCommand(ctx, "domain", link, resolvedRouteOnlyDomain); err != nil {
		return Step{}, fmt.Errorf("configure systemd-resolved route-only DNS domain for %s: %w", link, err)
	}
	if err := e.runResolvedApplyCommand(ctx, "default-route", link, "yes"); err != nil {
		return Step{}, fmt.Errorf("configure systemd-resolved DNS default route for %s: %w", link, err)
	}
	return Step{Kind: "dns", Target: link, Description: plan.Reason, Owner: OwnerDNS}, nil
}

func (e ResolvedDNSExecutor) runResolvedApplyCommand(ctx context.Context, args ...string) error {
	attempts := e.ApplyAttempts
	if attempts <= 0 {
		attempts = defaultResolvedApplyAttempts
	}
	pollInterval := e.ApplyPollInterval
	if pollInterval <= 0 {
		pollInterval = defaultResolvedApplyPollInterval
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err := observeCommand(ctx, e.Runner, "resolvectl", args...)
		if err == nil {
			return nil
		}
		lastErr = err
		if !resolvedCommandResultIsMissing(ctx, result, err) || attempt == attempts {
			return err
		}
		if err := sleepResolvedDNSPoll(ctx, e.Sleep, pollInterval); err != nil {
			return errors.Join(lastErr, err)
		}
	}
	return lastErr
}

// Verify checks that the target link keeps the planned DNS servers, route-only
// domain, and DNS default-route setting after apply. Current Scopes is not an
// ownership or configuration check: systemd-resolved derives it from active
// lookup scope state and may report none while the per-link configuration is
// already present. Transient missing link/server/domain/default-route observations
// are polled for a bounded period instead of failing immediately.
func (e ResolvedDNSExecutor) Verify(ctx context.Context, plan planner.TunDNSPlan) error {
	if err := validateDNSPlan(plan); err != nil {
		return err
	}
	link := strings.TrimSpace(plan.TargetLink)
	attempts := e.VerifyAttempts
	if attempts <= 0 {
		attempts = defaultResolvedVerifyAttempts
	}
	pollInterval := e.VerifyPollInterval
	if pollInterval <= 0 {
		pollInterval = defaultResolvedVerifyPollInterval
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		err := e.verifyResolvedDNSOnce(ctx, link, plan)
		if err == nil {
			return nil
		}
		lastErr = err
		var verifyErr resolvedDNSVerifyError
		if !errors.As(err, &verifyErr) || !verifyErr.retryable || attempt == attempts {
			return err
		}
		if err := sleepResolvedDNSPoll(ctx, e.Sleep, pollInterval); err != nil {
			return errors.Join(lastErr, err)
		}
	}
	return lastErr
}

func (e ResolvedDNSExecutor) verifyResolvedDNSOnce(ctx context.Context, link string, plan planner.TunDNSPlan) error {
	result, err := observeCommand(ctx, e.Runner, "resolvectl", "status", "--no-pager")
	if err != nil {
		return fmt.Errorf("verify systemd-resolved DNS for %s: %w", link, err)
	}
	links := netsnapshot.ParseResolvedLinks(result.Stdout)
	if foreign, ok := findForeignRouteOnlyDNSOwner(links, link); ok {
		return newResolvedDNSVerifyError(link, false, "foreign route-only DNS owner %s still has %s", foreign.Name, resolvedRouteOnlyDomain)
	}
	targetLinks := findResolvedLinks(links, link)
	if len(targetLinks) == 0 {
		return newResolvedDNSVerifyError(link, true, "link status not found")
	}
	if len(targetLinks) != 1 {
		return newResolvedDNSVerifyError(link, false, "duplicate target link status records: %d", len(targetLinks))
	}
	if mismatch := resolvedLinkMismatch(targetLinks[0], plan); mismatch != "" {
		return newResolvedDNSVerifyError(link, true, "%s", mismatch)
	}
	return nil
}

func resolvedLinkMismatch(link netsnapshot.ResolvedLink, plan planner.TunDNSPlan) string {
	for _, server := range plan.Servers {
		if !containsDNSValue(link.DNSServers, server) {
			return fmt.Sprintf("DNS server %s not found", server)
		}
	}
	if !containsDNSValue(link.DNSDomains, resolvedRouteOnlyDomain) {
		return fmt.Sprintf("route-only domain %s not found", resolvedRouteOnlyDomain)
	}
	if !containsDNSValue(link.Protocols, "+DefaultRoute") {
		return "DNS default route is not enabled"
	}
	return ""
}

type resolvedDNSVerifyError struct {
	message   string
	retryable bool
}

func (e resolvedDNSVerifyError) Error() string {
	return e.message
}

func newResolvedDNSVerifyError(link string, retryable bool, format string, args ...any) error {
	return resolvedDNSVerifyError{
		message:   fmt.Sprintf("verify systemd-resolved DNS for %s: %s", link, fmt.Sprintf(format, args...)),
		retryable: retryable,
	}
}

func sleepResolvedDNSPoll(ctx context.Context, sleep func(context.Context, time.Duration) error, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	if sleep != nil {
		return sleep(ctx, duration)
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

func findForeignRouteOnlyDNSOwner(links []netsnapshot.ResolvedLink, targetLink string) (netsnapshot.ResolvedLink, bool) {
	for _, link := range links {
		if strings.TrimSpace(link.Name) == "" || link.Name == targetLink {
			continue
		}
		if !containsDNSValue(link.DNSDomains, resolvedRouteOnlyDomain) {
			continue
		}
		if containsDNSValue(link.CurrentScopes, "DNS") || containsDNSValue(link.Protocols, "+DefaultRoute") || strings.TrimSpace(link.CurrentDNSServer) != "" || len(link.DNSServers) > 0 {
			return link, true
		}
	}
	return netsnapshot.ResolvedLink{}, false
}

func findResolvedLinks(links []netsnapshot.ResolvedLink, name string) []netsnapshot.ResolvedLink {
	matches := make([]netsnapshot.ResolvedLink, 0, 1)
	for _, link := range links {
		if link.Name == name {
			matches = append(matches, link)
		}
	}
	return matches
}

func containsDNSValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// Rollback reverts all systemd-resolved per-link state for the podlaz link.
func (e ResolvedDNSExecutor) Rollback(ctx context.Context, plan planner.TunDNSPlan) error {
	link := strings.TrimSpace(plan.TargetLink)
	if link == "" {
		return nil
	}
	result, err := observeCommand(ctx, e.Runner, "resolvectl", "revert", link)
	if err != nil && !resolvedCommandResultIsMissing(ctx, result, err) {
		return fmt.Errorf("revert systemd-resolved DNS for %s: %w", link, err)
	}
	return nil
}

func validateDNSPlan(plan planner.TunDNSPlan) error {
	if plan.Action == planner.DNSActionBlocked {
		return fmt.Errorf("DNS desired state is blocked: %s", plan.Reason)
	}
	if plan.Action != "" && plan.Action != planner.DNSActionConfigure {
		return fmt.Errorf("unsupported DNS action %q", plan.Action)
	}
	if strings.TrimSpace(plan.TargetLink) == "" {
		return errors.New("missing DNS target link")
	}
	if len(plan.Servers) == 0 {
		return errors.New("missing DNS servers")
	}
	if plan.Backend != "" && plan.Backend != planner.DNSBackendSystemdResolved {
		return fmt.Errorf("unsupported DNS backend %q", plan.Backend)
	}
	return nil
}

func shouldApplyDNS(plan planner.TunDNSPlan) bool {
	return plan.Action == planner.DNSActionConfigure && strings.TrimSpace(plan.TargetLink) != ""
}

func hasFirewallPlan(plan planner.TunFirewallPlan) bool {
	return strings.TrimSpace(plan.Backend) != "" || strings.TrimSpace(plan.Family) != "" || strings.TrimSpace(plan.Table) != "" || strings.TrimSpace(plan.TableAction) != ""
}
