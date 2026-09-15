package e2e_test

import "testing"

func TestHostedE2ECapabilityClassifiesDirectUDPToPlannedDNSPrivately(t *testing.T) {
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)
	requireHostedCapabilityMarkers(t, workflow,
		"profile-direct-udp-dns.log",
		"tun.resolved_query.udp_dns_response=observed",
		"tun.resolved_query.udp_dns_timeout=observed",
		"tun.resolved_query.udp_dns_error=observed",
	)
	forbidHostedCapabilityMarkers(t, workflow,
		"cat /tmp/podlaz-capability-udp-dns",
		"print(data)",
		"print(response)",
	)
}
