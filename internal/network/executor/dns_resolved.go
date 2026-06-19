package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	OwnerDNS                = "podlaz:dns-link"
	resolvedRouteOnlyDomain = "~."
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

// NewOSDNSExecutor returns the Linux iproute2 + systemd-resolved + nftables executor.
func NewOSDNSExecutor() DNSAwareTunExecutor {
	runner := OSRunner{}
	return DNSAwareTunExecutor{
		Base:     NewOSExecutor(),
		DNS:      ResolvedDNSExecutor{Runner: runner},
		Firewall: NftablesExecutor{Runner: runner},
	}
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
}

// Apply configures the DNS servers, route-only default DNS domain, and per-link DNS default route.
func (e ResolvedDNSExecutor) Apply(ctx context.Context, plan planner.TunDNSPlan) (Step, error) {
	if err := validateDNSPlan(plan); err != nil {
		return Step{}, err
	}
	link := strings.TrimSpace(plan.TargetLink)
	args := append([]string{"dns", link}, plan.Servers...)
	if err := runCommand(ctx, e.Runner, "resolvectl", args...); err != nil {
		return Step{}, fmt.Errorf("configure systemd-resolved DNS server for %s: %w", link, err)
	}
	if err := runCommand(ctx, e.Runner, "resolvectl", "domain", link, resolvedRouteOnlyDomain); err != nil {
		return Step{}, fmt.Errorf("configure systemd-resolved route-only DNS domain for %s: %w", link, err)
	}
	if err := runCommand(ctx, e.Runner, "resolvectl", "default-route", link, "yes"); err != nil {
		return Step{}, fmt.Errorf("configure systemd-resolved DNS default route for %s: %w", link, err)
	}
	return Step{Kind: "dns", Target: link, Description: plan.Reason, Owner: OwnerDNS}, nil
}

// Verify checks that the target link exposes planned DNS servers and route-only domain after apply.
func (e ResolvedDNSExecutor) Verify(ctx context.Context, plan planner.TunDNSPlan) error {
	if err := validateDNSPlan(plan); err != nil {
		return err
	}
	link := strings.TrimSpace(plan.TargetLink)
	result, err := observeCommand(ctx, e.Runner, "resolvectl", "status", link, "--no-pager")
	if err != nil {
		return fmt.Errorf("verify systemd-resolved DNS for %s: %w", link, err)
	}
	for _, server := range plan.Servers {
		if !strings.Contains(result.Stdout, server) {
			return fmt.Errorf("verify systemd-resolved DNS for %s: DNS server %s not found", link, server)
		}
	}
	if !strings.Contains(result.Stdout, resolvedRouteOnlyDomain) {
		return fmt.Errorf("verify systemd-resolved DNS for %s: route-only domain %s not found", link, resolvedRouteOnlyDomain)
	}
	return nil
}

// Rollback reverts all systemd-resolved per-link state for the podlaz link.
func (e ResolvedDNSExecutor) Rollback(ctx context.Context, plan planner.TunDNSPlan) error {
	link := strings.TrimSpace(plan.TargetLink)
	if link == "" {
		return nil
	}
	if err := runCommand(ctx, e.Runner, "resolvectl", "revert", link); err != nil && !resourceMissing(err) {
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
