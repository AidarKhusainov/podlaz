package executor

import (
	"context"
	"strings"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

// DefaultDNSOwnershipError is an evidence-based blocker, not cleanup authority.
// Unknown means resolved could not be inspected conclusively, rather than
// proof that another service owns the default DNS route.
type DefaultDNSOwnershipError struct {
	Link    string
	Unknown bool
}

func (e *DefaultDNSOwnershipError) Error() string {
	if e == nil || e.Unknown {
		return "default DNS ownership could not be verified"
	}
	return "another VPN or network service controls default DNS (~.) on " + safeDNSLinkName(e.Link)
}

func safeDNSLinkName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 15 {
		return "another interface"
	}
	for _, char := range name {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.') {
			return "another interface"
		}
	}
	return name
}

// CheckDefaultDNSOwnershipSnapshot checks an existing read-only resolver
// observation. A foreign tunnel or split-domain DNS is not by itself a
// conflicting owner. Only a foreign route-only default domain with stable
// routing/configuration evidence establishes the conflict.
func CheckDefaultDNSOwnershipSnapshot(dns netsnapshot.DNS, targetLink string) error {
	if dns.Resolved.Status != netsnapshot.StatusDetected ||
		(dns.Mode != "" && dns.Mode != "systemd-resolved") ||
		strings.TrimSpace(targetLink) == "" {
		return &DefaultDNSOwnershipError{Unknown: true}
	}
	return checkDefaultDNSOwnershipLinks(dns.ResolvedLinks, targetLink)
}

func checkDefaultDNSOwnershipLinks(links []netsnapshot.ResolvedLink, targetLink string) error {
	if foreign, ok := findForeignRouteOnlyDNSOwner(links, targetLink); ok {
		return &DefaultDNSOwnershipError{Link: foreign.Name}
	}
	return nil
}

// CheckDefaultDNSOwnership observes the *current* systemd-resolved state.
// The caller must invoke it again at mutation boundaries; a previous snapshot
// is never a lease on ownership. No foreign link is ever changed by this check.
func (e ResolvedDNSExecutor) CheckDefaultDNSOwnership(ctx context.Context, targetLink string) error {
	result, err := observeCommand(ctx, e.Runner, "resolvectl", "status", "--no-pager")
	if err != nil || strings.TrimSpace(result.Stdout) == "" {
		return &DefaultDNSOwnershipError{Unknown: true}
	}
	links := netsnapshot.ParseResolvedLinks(result.Stdout)
	headers := 0
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Link ") {
			headers++
		}
	}
	if headers != len(links) || (headers == 0 && !strings.Contains(result.Stdout, "Global")) {
		return &DefaultDNSOwnershipError{Unknown: true}
	}
	return checkDefaultDNSOwnershipLinks(links, targetLink)
}
