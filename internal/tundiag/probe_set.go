package tundiag

import (
	"context"
	"time"
)

type ProbeFunc func(context.Context) ProbeResult

type ProbeAdapters struct {
	Session           ProbeFunc
	ServerBypass      ProbeFunc
	IPv4Route         ProbeFunc
	DNSState          ProbeFunc
	DNSUDP            ProbeFunc
	DNSTCP            ProbeFunc
	SystemResolver    ProbeFunc
	NXDomainIntegrity ProbeFunc
	TCP443            ProbeFunc
	TLS               ProbeFunc
	HTTPSCloudflare   ProbeFunc
	HTTPSGoogle       ProbeFunc
	DoHCloudflare     ProbeFunc
	DoHGoogle         ProbeFunc
	IPv6              ProbeFunc
	PMTUCloudflare    ProbeFunc
	PMTUHetzner       ProbeFunc
}

func StandardProbes(adapters ProbeAdapters) []Probe {
	return []Probe{
		standardProbe("session", LayerSession, "active podlaz TUN session", 2*time.Second, nil, adapters.Session),
		standardProbe("server-bypass", LayerBypass, "VPN server endpoint", 2*time.Second, []string{"session"}, adapters.ServerBypass),
		standardProbe("route-ipv4", LayerRoute, "1.1.1.1", 2*time.Second, []string{"session"}, adapters.IPv4Route),
		standardProbe("dns-state", LayerDNS, "systemd-resolved podlaz0", 3*time.Second, []string{"route-ipv4"}, adapters.DNSState),
		standardProbe("dns-udp", LayerDNS, "configured DNS server UDP/53", 4*time.Second, []string{"dns-state"}, adapters.DNSUDP),
		standardProbe("dns-tcp", LayerDNS, "configured DNS server TCP/53", 4*time.Second, []string{"dns-state"}, adapters.DNSTCP),
		standardProbe("dns-system-resolution", LayerDNS, "example.com", 4*time.Second, []string{"dns-state"}, adapters.SystemResolver),
		standardProbe("dns-nxdomain-integrity", LayerDNS, "podlaz-diagnostic.invalid", 4*time.Second, []string{"dns-state"}, adapters.NXDomainIntegrity),
		standardProbe("tcp-443", LayerTCP, "www.cloudflare.com:443", 4*time.Second, []string{"route-ipv4"}, adapters.TCP443),
		standardProbe("tls", LayerTLS, "www.cloudflare.com:443", 5*time.Second, []string{"tcp-443"}, adapters.TLS),
		standardProbe("https-cloudflare-small", LayerHTTPS, "https://www.cloudflare.com/cdn-cgi/trace", 6*time.Second, []string{"tcp-443"}, adapters.HTTPSCloudflare),
		standardProbe("https-google-small", LayerHTTPS, "https://www.google.com/generate_204", 6*time.Second, []string{"route-ipv4"}, adapters.HTTPSGoogle),
		standardProbe("doh-cloudflare", LayerDoH, "https://cloudflare-dns.com/dns-query", 6*time.Second, []string{"route-ipv4"}, adapters.DoHCloudflare),
		standardProbe("doh-google", LayerDoH, "https://dns.google/dns-query", 6*time.Second, []string{"route-ipv4"}, adapters.DoHGoogle),
		standardProbe("ipv6", LayerIPv6, "host IPv6 state", 3*time.Second, []string{"session"}, adapters.IPv6),
		standardProbe("pmtu-cloudflare-16k", LayerPMTU, "https://speed.cloudflare.com/__down?bytes=16384", 8*time.Second, []string{"https-cloudflare-small"}, adapters.PMTUCloudflare),
		standardProbe("pmtu-hetzner-16k", LayerPMTU, "https://speed.hetzner.de/100MB.bin", 8*time.Second, []string{"https-cloudflare-small"}, adapters.PMTUHetzner),
	}
}

func standardProbe(id string, layer Layer, target string, timeout time.Duration, dependencies []string, run ProbeFunc) Probe {
	return Probe{
		Definition: ProbeDefinition{ID: id, Layer: layer, Target: target, Timeout: timeout, DependsOn: dependencies},
		Run: func(ctx context.Context) ProbeResult {
			if run == nil {
				return ProbeResult{Status: ProbeSkipped, DependencyReason: "probe adapter is unavailable"}
			}
			return run(ctx)
		},
	}
}
