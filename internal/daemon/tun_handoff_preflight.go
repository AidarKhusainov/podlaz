package daemon

import (
	"errors"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

const defaultDNSRouteDomain = "~."

type tunHandoffBlocker struct {
	Interface string
	DNSDomain string
	DNSServer string
	Policy    string
}

func (e *tunHandoffBlocker) Error() string {
	if e == nil {
		return "podlaz: TUN handoff blocked"
	}
	return fmt.Sprintf(`podlaz: another VPN appears to own default DNS routing.

Detected:
  interface: %s
  DNS domain: %s
  DNS server: %s

podlaz did not change network state.
Stop the other VPN first, then retry.

Run:
  plz doctor
  resolvectl status`, fallbackUnknown(e.Interface), fallbackUnknown(e.DNSDomain), fallbackUnknown(e.DNSServer))
}

type tunStalePodlazStateBlocker struct {
	Resources []string
}

func (e *tunStalePodlazStateBlocker) Error() string {
	if e == nil || len(e.Resources) == 0 {
		return "podlaz: stale podlaz-owned networking state blocks TUN connect"
	}
	return fmt.Sprintf(`podlaz: stale podlaz-owned networking state blocks TUN connect.

Detected:
  - %s

podlaz did not change network state.
Run daemon-owned recovery first, then retry connect.

Run:
  plz recover --execute --yes`, strings.Join(e.Resources, "\n  - "))
}

func preflightTunOwnership(s netsnapshot.Snapshot, handoff string) error {
	if blocker := stalePodlazStateBlocker(s); blocker != nil {
		return blocker
	}
	policy := api.NormalizeHandoffPolicy(handoff)
	foreign, ok := foreignDefaultDNSOwner(s)
	if !ok {
		return nil
	}
	blocker := &tunHandoffBlocker{
		Interface: foreign.Name,
		DNSDomain: firstOrDefault(foreign.DNSDomains, defaultDNSRouteDomain),
		DNSServer: firstNonEmpty(foreign.CurrentDNSServer, firstOrDefault(foreign.DNSServers, "")),
		Policy:    policy,
	}
	return blocker
}

func isTunHandoffBlocker(err error) bool {
	var blocker *tunHandoffBlocker
	return errors.As(err, &blocker)
}

func isTunStalePodlazStateBlocker(err error) bool {
	var blocker *tunStalePodlazStateBlocker
	return errors.As(err, &blocker)
}

func stalePodlazStateBlocker(s netsnapshot.Snapshot) *tunStalePodlazStateBlocker {
	resources := stalePodlazResourceSummaries(s)
	if len(resources) == 0 {
		return nil
	}
	return &tunStalePodlazStateBlocker{Resources: resources}
}

func stalePodlazResourceSummaries(s netsnapshot.Snapshot) []string {
	seen := map[string]bool{}
	var resources []string
	add := func(kind, name string) {
		kind = strings.TrimSpace(kind)
		name = strings.TrimSpace(name)
		if kind == "" || name == "" {
			return
		}
		value := kind + " " + name
		if seen[value] {
			return
		}
		seen[value] = true
		resources = append(resources, value)
	}
	for _, resource := range s.StaleResources {
		if resource.Status == netsnapshot.StatusDetected {
			add(resource.Kind, resource.Name)
		}
	}
	for _, device := range s.TunDevices {
		if device.Name == netsnapshot.DefaultTunName && device.Status == netsnapshot.StatusDetected {
			add("tun-device", device.Name)
		}
	}
	if s.Nftables.PodlazTable.Status == netsnapshot.StatusDetected {
		add("nftables-table", netsnapshot.DefaultNFTFamily+" "+netsnapshot.DefaultNFTTable)
	}
	return resources
}

func foreignDefaultDNSOwner(s netsnapshot.Snapshot) (netsnapshot.ResolvedLink, bool) {
	for _, link := range s.DNS.ResolvedLinks {
		if strings.TrimSpace(link.Name) == "" || link.Name == netsnapshot.DefaultTunName {
			continue
		}
		if !containsToken(link.DNSDomains, defaultDNSRouteDomain) {
			continue
		}
		if containsToken(link.CurrentScopes, "DNS") || containsToken(link.Protocols, "+DefaultRoute") || strings.TrimSpace(link.CurrentDNSServer) != "" || len(link.DNSServers) > 0 {
			return link, true
		}
	}
	return netsnapshot.ResolvedLink{}, false
}

func containsToken(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func fallbackUnknown(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<unknown>"
	}
	return value
}

func firstOrDefault(values []string, fallback string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
