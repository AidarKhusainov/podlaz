package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	privacyEnvelopeCompositionVersion = 1
	privacyEnvelopeCandidateLimit     = 16
	privacyEnvelopeFamily             = "inet"
	privacyEnvelopeOutputChain        = "output"
	privacyEnvelopeOutputPriority     = -10
	privacyEnvelopeReason             = "preserve a fail-closed network session privacy boundary"
)

type privacyEnvelopeObserver interface {
	PrivacyEnvelopeTableExists(context.Context, string, string) (bool, error)
}

func allocatePrivacyEnvelope(
	ctx context.Context,
	sessionID string,
	tunInterface string,
	bootstrapIPv4 []string,
	observer privacyEnvelopeObserver,
) (networkSessionProtection, netexecutor.PrivacyEnvelopePlan, error) {
	if !networkSessionIDPattern.MatchString(sessionID) {
		return networkSessionProtection{}, netexecutor.PrivacyEnvelopePlan{}, errors.New("invalid network session identity for privacy envelope allocation")
	}
	if err := validateNetworkSessionInterface(tunInterface); err != nil {
		return networkSessionProtection{}, netexecutor.PrivacyEnvelopePlan{}, err
	}
	endpoints, err := normalizePrivacyEnvelopeBootstrapIPv4(bootstrapIPv4)
	if err != nil {
		return networkSessionProtection{}, netexecutor.PrivacyEnvelopePlan{}, err
	}
	if observer == nil {
		return networkSessionProtection{}, netexecutor.PrivacyEnvelopePlan{}, errors.New("privacy envelope allocation requires authoritative table observation")
	}

	baseTable := "podlaz_pe_" + sessionID[:12]
	for candidateIndex := 0; candidateIndex < privacyEnvelopeCandidateLimit; candidateIndex++ {
		table := baseTable
		if candidateIndex != 0 {
			table = fmt.Sprintf("%s_%d", baseTable, candidateIndex)
		}
		occupied, observeErr := observer.PrivacyEnvelopeTableExists(ctx, privacyEnvelopeFamily, table)
		if observeErr != nil {
			return networkSessionProtection{}, netexecutor.PrivacyEnvelopePlan{}, fmt.Errorf("observe privacy envelope candidate %s %s: %w", privacyEnvelopeFamily, table, observeErr)
		}
		if occupied {
			continue
		}

		protection := networkSessionProtection{
			State:              networkSessionProtectionArming,
			CompositionVersion: privacyEnvelopeCompositionVersion,
			Family:             privacyEnvelopeFamily,
			Table:              table,
			TunInterface:       tunInterface,
			BootstrapIPv4:      endpoints,
		}
		plan, planErr := privacyEnvelopePlanFromAuthority(protection)
		if planErr != nil {
			return networkSessionProtection{}, netexecutor.PrivacyEnvelopePlan{}, fmt.Errorf("build allocated privacy envelope plan: %w", planErr)
		}
		return protection, plan, nil
	}

	return networkSessionProtection{}, netexecutor.PrivacyEnvelopePlan{}, fmt.Errorf("no collision-free privacy envelope table available in bounded candidate set of %d", privacyEnvelopeCandidateLimit)
}

func privacyEnvelopePlanFromAuthority(protection networkSessionProtection) (netexecutor.PrivacyEnvelopePlan, error) {
	if err := validateNetworkSessionProtection(protection); err != nil {
		return netexecutor.PrivacyEnvelopePlan{}, err
	}
	if protection.CompositionVersion != privacyEnvelopeCompositionVersion {
		return netexecutor.PrivacyEnvelopePlan{}, fmt.Errorf("unsupported privacy envelope composition version %d", protection.CompositionVersion)
	}

	plan := netexecutor.PrivacyEnvelopePlan{
		Family: protection.Family,
		Table:  protection.Table,
		Chains: []planner.TunFirewallChainPlan{{
			Name:     privacyEnvelopeOutputChain,
			Type:     planner.FirewallChainTypeFilter,
			Hook:     planner.FirewallOutputHook,
			Priority: privacyEnvelopeOutputPriority,
			Policy:   planner.FirewallDefaultChainPolicy,
			Action:   planner.FirewallActionAdd,
			Reason:   privacyEnvelopeReason,
		}},
		Reason: privacyEnvelopeReason,
	}

	appendAccept := func(expr, ownership string) {
		plan.Rules = append(plan.Rules, planner.TunFirewallRulePlan{
			Chain:     privacyEnvelopeOutputChain,
			Expr:      expr,
			Verdict:   planner.FirewallVerdictAccept,
			Action:    planner.FirewallActionAdd,
			Reason:    privacyEnvelopeReason,
			Ownership: ownership,
		})
	}

	appendAccept(`oifname "lo"`, "podlaz:privacy-envelope:loopback")
	appendAccept(fmt.Sprintf(`oifname %q`, protection.TunInterface), "podlaz:privacy-envelope:tun-egress")
	for _, endpoint := range protection.BootstrapIPv4 {
		appendAccept("ip daddr "+endpoint, "podlaz:privacy-envelope:bootstrap")
	}
	appendAccept("meta nfproto ipv4 udp sport 68 udp dport 67", "podlaz:privacy-envelope:dhcp4")
	appendAccept("meta nfproto ipv6 udp sport 546 udp dport 547", "podlaz:privacy-envelope:dhcp6")
	appendAccept("meta nfproto ipv6 icmpv6 type { nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert }", "podlaz:privacy-envelope:ipv6-link-control")
	plan.Rules = append(plan.Rules, planner.TunFirewallRulePlan{
		Chain:     privacyEnvelopeOutputChain,
		Verdict:   planner.FirewallVerdictReject,
		Action:    planner.FirewallActionAdd,
		Reason:    privacyEnvelopeReason,
		Ownership: "podlaz:privacy-envelope:block-direct",
	})
	return plan, nil
}

func normalizePrivacyEnvelopeBootstrapIPv4(rawEndpoints []string) ([]string, error) {
	if len(rawEndpoints) == 0 {
		return nil, errors.New("privacy envelope requires at least one exact bootstrap IPv4 endpoint")
	}
	seen := make(map[string]struct{}, len(rawEndpoints))
	endpoints := make([]string, 0, len(rawEndpoints))
	for _, raw := range rawEndpoints {
		ip := net.ParseIP(strings.TrimSpace(raw))
		if ip == nil || ip.To4() == nil {
			return nil, errors.New("privacy envelope bootstrap endpoint must be a concrete IPv4 address")
		}
		normalized := ip.To4().String()
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		endpoints = append(endpoints, normalized)
	}
	if len(endpoints) == 0 {
		return nil, errors.New("privacy envelope requires at least one exact bootstrap IPv4 endpoint")
	}
	sort.Strings(endpoints)
	return endpoints, nil
}
