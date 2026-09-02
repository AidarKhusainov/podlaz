package tundiag

import "testing"

func TestIssue238IPv4OnlyHealthyPathIsNotGloballyUnhealthyForOneProviderTimeout(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "dns-system-positive", Layer: LayerDNS, Status: ProbePass},
		{ID: "https-google-small", Layer: LayerHTTPS, Status: ProbePass},
		{ID: "doh-cloudflare", Layer: LayerDoH, Status: ProbePass},
		{ID: "doh-google", Layer: LayerDoH, Status: ProbePass},
		{ID: "tcp-443", Layer: LayerTCP, Status: ProbeFail, Classification: ClassTimeout, FailurePhase: FailurePhaseTCPConnect},
		{ID: "ipv6", Layer: LayerIPv6, Status: ProbeFail, Classification: ClassIPv6NotPresent},
	}})

	if report.Status != StatusDegraded {
		t.Fatalf("working IPv4-only TUN path with one provider timeout must be degraded, got %#v", report)
	}
	if report.PrimaryClassification != ClassHTTPSPartialFailure {
		t.Fatalf("provider outage must remain a partial failure, got %#v", report)
	}
	for _, id := range []string{"dns-system-positive", "https-google-small", "doh-cloudflare", "doh-google"} {
		probe, ok := report.Probe(id)
		if !ok || probe.Status != ProbePass {
			t.Fatalf("required independent success %s was lost: %#v", id, probe)
		}
	}
}
