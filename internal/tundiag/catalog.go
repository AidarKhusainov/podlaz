package tundiag

import (
	"sort"
	"time"
)

type TargetKind string

const (
	TargetRoute TargetKind = "route"
	TargetDNS   TargetKind = "dns"
	TargetTCP   TargetKind = "tcp"
	TargetTLS   TargetKind = "tls"
	TargetHTTPS TargetKind = "https"
	TargetDoH   TargetKind = "doh"
	TargetIPv6  TargetKind = "ipv6"
	TargetPMTU  TargetKind = "pmtu"
)

type Target struct {
	ID                  string        `json:"id"`
	Kind                TargetKind    `json:"kind"`
	Purpose             string        `json:"purpose"`
	Method              string        `json:"method"`
	ExpectedSuccess     string        `json:"expected_success"`
	ExpectedStatusCodes []int         `json:"expected_status_codes,omitempty"`
	Host                string        `json:"host,omitempty"`
	Port                uint16        `json:"port,omitempty"`
	URL                 string        `json:"url,omitempty"`
	BootstrapAddresses  []string      `json:"bootstrap_addresses,omitempty"`
	Timeout             time.Duration `json:"-"`
	MaxResponseBytes    int64         `json:"max_response_bytes,omitempty"`
	PrivacyNote         string        `json:"privacy_note"`
	Required            bool          `json:"required"`
}

func Catalog() []Target {
	out := append([]Target(nil), targetCatalog...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func FindTarget(id string) (Target, bool) {
	for _, target := range targetCatalog {
		if target.ID == id {
			return target, true
		}
	}
	return Target{}, false
}

func TargetsByKind(kind TargetKind) []Target {
	var out []Target
	for _, target := range targetCatalog {
		if target.Kind == kind {
			out = append(out, target)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

var targetCatalog = []Target{
	{
		ID: "route-ipv4-primary", Kind: TargetRoute,
		Purpose: "Verify the effective IPv4 route through the active TUN session.", Method: "local ip route lookup",
		ExpectedSuccess: "route resolves through podlaz0 and the podlaz policy table", Host: "1.1.1.1",
		Timeout: 2 * time.Second, PrivacyNote: "Local kernel route lookup only; no packet is sent.", Required: true,
	},
	{
		ID: "dns-system-positive", Kind: TargetDNS,
		Purpose: "Verify positive DNS resolution through the configured resolver path.", Method: "DNS A query over UDP, TCP, and the system resolver",
		ExpectedSuccess: "NOERROR response with at least one address", Host: "example.com",
		Timeout: 4 * time.Second, PrivacyNote: "The configured resolver receives one A query for the RFC 2606 example domain.", Required: true,
	},
	{
		ID: "dns-system-nxdomain", Kind: TargetDNS,
		Purpose: "Detect DNS interception or hijacking.", Method: "system resolver lookup",
		ExpectedSuccess: "NXDOMAIN for a reserved .invalid name", Host: "podlaz-diagnostic.invalid",
		Timeout: 4 * time.Second, PrivacyNote: "The configured resolver receives one A query under the reserved .invalid domain.", Required: true,
	},
	{
		ID: "tcp-443-cloudflare", Kind: TargetTCP,
		Purpose: "Verify generic TCP/443 reachability through the tunnel.", Method: "bounded TCP connect",
		ExpectedSuccess: "TCP connection established before the deadline", Host: "www.cloudflare.com", Port: 443,
		Timeout: 4 * time.Second, PrivacyNote: "Cloudflare observes one bounded TCP connection attempt.", Required: true,
	},
	{
		ID: "tls-cloudflare", Kind: TargetTLS,
		Purpose: "Verify TLS negotiation and certificate validation.", Method: "TLS handshake with SNI and platform trust roots",
		ExpectedSuccess: "validated TLS 1.2 or newer handshake", Host: "www.cloudflare.com", Port: 443,
		Timeout: 5 * time.Second, PrivacyNote: "Cloudflare observes one bounded TLS handshake with normal certificate validation.", Required: true,
	},
	{
		ID: "https-cloudflare-small", Kind: TargetHTTPS,
		Purpose: "Verify a small HTTPS response through the tunnel.", Method: "GET without following redirects",
		ExpectedSuccess: "HTTP 200 with at most 4096 response bytes", ExpectedStatusCodes: []int{200},
		Host: "www.cloudflare.com", Port: 443, URL: "https://www.cloudflare.com/cdn-cgi/trace",
		Timeout: 6 * time.Second, MaxResponseBytes: 4096,
		PrivacyNote: "Cloudflare receives one bounded HTTPS connectivity request.", Required: true,
	},
	{
		ID: "https-google-small", Kind: TargetHTTPS,
		Purpose: "Corroborate small HTTPS reachability with an independent provider.", Method: "GET without following redirects",
		ExpectedSuccess: "HTTP 204 with an empty response", ExpectedStatusCodes: []int{204},
		Host: "www.google.com", Port: 443, URL: "https://www.google.com/generate_204",
		Timeout: 6 * time.Second, MaxResponseBytes: 1024,
		PrivacyNote: "Google receives one bounded HTTPS connectivity request.", Required: true,
	},
	{
		ID: "doh-cloudflare", Kind: TargetDoH,
		Purpose: "Verify RFC 8484 DNS-over-HTTPS independently of system DNS.", Method: "POST application/dns-message over validated HTTPS",
		ExpectedSuccess: "HTTP 200 and a valid NOERROR DNS response", ExpectedStatusCodes: []int{200},
		Host: "cloudflare-dns.com", Port: 443, URL: "https://cloudflare-dns.com/dns-query",
		BootstrapAddresses: []string{"1.1.1.1", "1.0.0.1"}, Timeout: 6 * time.Second, MaxResponseBytes: 4096,
		PrivacyNote: "Cloudflare receives one RFC 8484 DNS-over-HTTPS query for example.com.", Required: false,
	},
	{
		ID: "doh-google", Kind: TargetDoH,
		Purpose: "Corroborate RFC 8484 DNS-over-HTTPS with an independent provider.", Method: "POST application/dns-message over validated HTTPS",
		ExpectedSuccess: "HTTP 200 and a valid NOERROR DNS response", ExpectedStatusCodes: []int{200},
		Host: "dns.google", Port: 443, URL: "https://dns.google/dns-query",
		BootstrapAddresses: []string{"8.8.8.8", "8.8.4.4"}, Timeout: 6 * time.Second, MaxResponseBytes: 4096,
		PrivacyNote: "Google Public DNS receives one RFC 8484 DNS-over-HTTPS query for example.com.", Required: false,
	},
	{
		ID: "ipv6-cloudflare", Kind: TargetIPv6,
		Purpose: "Verify that a detected IPv6 path is usable and follows the observed route.", Method: "AAAA lookup, local IPv6 route lookup, and bounded TCP/443 connect",
		ExpectedSuccess: "an IPv6 address resolves, routes consistently, and accepts TCP/443", Host: "www.cloudflare.com", Port: 443,
		Timeout: 5 * time.Second, PrivacyNote: "Cloudflare observes one bounded IPv6 TCP connection attempt when IPv6 is present.", Required: false,
	},
	{
		ID: "pmtu-cloudflare-16k", Kind: TargetPMTU,
		Purpose: "Collect bounded larger-transfer evidence for PMTU diagnosis.", Method: "GET capped at 16 KiB without redirects",
		ExpectedSuccess: "HTTP 200 and exactly 16384 response bytes", ExpectedStatusCodes: []int{200},
		Host: "speed.cloudflare.com", Port: 443, URL: "https://speed.cloudflare.com/__down?bytes=16384",
		Timeout: 8 * time.Second, MaxResponseBytes: 16384,
		PrivacyNote: "Cloudflare receives one bounded 16 KiB HTTPS transfer request.", Required: false,
	},
	{
		ID: "pmtu-hetzner-16k", Kind: TargetPMTU,
		Purpose: "Corroborate larger-transfer behavior with an independent provider.", Method: "HTTPS range GET capped at 16 KiB without redirects",
		ExpectedSuccess: "HTTP 200 or 206 and exactly 16384 response bytes", ExpectedStatusCodes: []int{200, 206},
		Host: "speed.hetzner.de", Port: 443, URL: "https://speed.hetzner.de/100MB.bin",
		Timeout: 8 * time.Second, MaxResponseBytes: 16384,
		PrivacyNote: "Hetzner receives one HTTPS range request capped at 16 KiB.", Required: false,
	},
}
