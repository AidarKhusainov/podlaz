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
Stop the other VPN first, or run with an explicit handoff option.

Run:
  plz doctor
  resolvectl status`, fallbackUnknown(e.Interface), fallbackUnknown(e.DNSDomain), fallbackUnknown(e.DNSServer))
}

func preflightTunOwnership(s netsnapshot.Snapshot, handoff string) error {
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
	switch policy {
	case api.HandoffBlock, api.HandoffAsk, api.HandoffStopKnown, api.HandoffReplacePodlaz:
		return blocker
	default:
		return blocker
	}
}

func isTunHandoffBlocker(err error) bool {
	var blocker *tunHandoffBlocker
	return errors.As(err, &blocker)
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
