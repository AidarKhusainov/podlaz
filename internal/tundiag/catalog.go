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
	TargetPMTU  TargetKind = "pmtu"
)

type Target struct {
	ID                 string        `json:"id"`
	Kind               TargetKind    `json:"kind"`
	Host               string        `json:"host,omitempty"`
	Port               uint16        `json:"port,omitempty"`
	URL                string        `json:"url,omitempty"`
	BootstrapAddresses []string      `json:"bootstrap_addresses,omitempty"`
	Timeout            time.Duration `json:"-"`
	MaxResponseBytes   int64         `json:"max_response_bytes,omitempty"`
	PrivacyNote        string        `json:"privacy_note"`
	Required           bool          `json:"required"`
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
		ID:          "route-ipv4-primary",
		Kind:        TargetRoute,
		Host:        "1.1.1.1",
		Timeout:     2 * time.Second,
		PrivacyNote: "Local kernel route lookup only; no packet is sent.",
		Required:    true,
	},
	{
		ID:          "dns-system-positive",
		Kind:        TargetDNS,
		Host:        "example.com",
		Timeout:     4 * time.Second,
		PrivacyNote: "The configured resolver receives one A query for the RFC 2606 example domain.",
		Required:    true,
	},
	{
		ID:          "dns-system-nxdomain",
		Kind:        TargetDNS,
		Host:        "podlaz-diagnostic.invalid",
		Timeout:     4 * time.Second,
		PrivacyNote: "The configured resolver receives one A query under the reserved .invalid domain.",
		Required:    true,
	},
	{
		ID:          "tcp-443-cloudflare",
		Kind:        TargetTCP,
		Host:        "www.cloudflare.com",
		Port:        443,
		Timeout:     4 * time.Second,
		PrivacyNote: "Cloudflare observes one bounded TCP connection attempt.",
		Required:    true,
	},
	{
		ID:          "tls-cloudflare",
		Kind:        TargetTLS,
		Host:        "www.cloudflare.com",
		Port:        443,
		Timeout:     5 * time.Second,
		PrivacyNote: "Cloudflare observes one bounded TLS handshake with normal certificate validation.",
		Required:    true,
	},
	{
		ID:               "https-cloudflare-small",
		Kind:             TargetHTTPS,
		Host:             "www.cloudflare.com",
		Port:             443,
		URL:              "https://www.cloudflare.com/cdn-cgi/trace",
		Timeout:          6 * time.Second,
		MaxResponseBytes: 4096,
		PrivacyNote:      "Cloudflare receives one bounded HTTPS connectivity request.",
		Required:         true,
	},
	{
		ID:               "https-google-small",
		Kind:             TargetHTTPS,
		Host:             "www.google.com",
		Port:             443,
		URL:              "https://www.google.com/generate_204",
		Timeout:          6 * time.Second,
		MaxResponseBytes: 1024,
		PrivacyNote:      "Google receives one bounded HTTPS connectivity request.",
		Required:         true,
	},
	{
		ID:                 "doh-cloudflare",
		Kind:               TargetDoH,
		Host:               "cloudflare-dns.com",
		Port:               443,
		URL:                "https://cloudflare-dns.com/dns-query",
		BootstrapAddresses: []string{"1.1.1.1", "1.0.0.1"},
		Timeout:            6 * time.Second,
		MaxResponseBytes:   4096,
		PrivacyNote:        "Cloudflare receives one RFC 8484 DNS-over-HTTPS query for example.com.",
		Required:           false,
	},
	{
		ID:                 "doh-google",
		Kind:               TargetDoH,
		Host:               "dns.google",
		Port:               443,
		URL:                "https://dns.google/dns-query",
		BootstrapAddresses: []string{"8.8.8.8", "8.8.4.4"},
		Timeout:            6 * time.Second,
		MaxResponseBytes:   4096,
		PrivacyNote:        "Google Public DNS receives one RFC 8484 DNS-over-HTTPS query for example.com.",
		Required:           false,
	},
	{
		ID:               "pmtu-cloudflare-16k",
		Kind:             TargetPMTU,
		Host:             "speed.cloudflare.com",
		Port:             443,
		URL:              "https://speed.cloudflare.com/__down?bytes=16384",
		Timeout:          8 * time.Second,
		MaxResponseBytes: 16384,
		PrivacyNote:      "Cloudflare receives one bounded 16 KiB HTTPS transfer request.",
		Required:         false,
	},
	{
		ID:               "pmtu-hetzner-16k",
		Kind:             TargetPMTU,
		Host:             "speed.hetzner.de",
		Port:             443,
		URL:              "https://speed.hetzner.de/100MB.bin",
		Timeout:          8 * time.Second,
		MaxResponseBytes: 16384,
		PrivacyNote:      "Hetzner receives one HTTPS range request capped at 16 KiB.",
		Required:         false,
	},
}
