package tundiag

import "testing"

func TestPreRollbackProbesExcludeOptionalLongRunningChecks(t *testing.T) {
	probes := PreRollbackProbes(ProbeAdapters{})
	got := make(map[string]bool, len(probes))
	for _, probe := range probes {
		got[probe.Definition.ID] = true
	}
	for _, required := range []string{"session", "server-bypass", "route-ipv4", "dns-state", "dns-udp", "dns-tcp", "dns-system-resolution", "tcp-443", "tls", "https-cloudflare-small"} {
		if !got[required] {
			t.Fatalf("required pre-rollback probe %q is missing", required)
		}
	}
	for _, forbidden := range []string{"dns-nxdomain-integrity", "https-google-small", "doh-cloudflare", "doh-google", "ipv6", "pmtu-cloudflare-16k", "pmtu-hetzner-16k"} {
		if got[forbidden] {
			t.Fatalf("optional probe %q must not delay rollback", forbidden)
		}
	}
}
