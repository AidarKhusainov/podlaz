package tundiag

import "testing"

func TestFinalizeDoesNotReportPMTUWhenLowerLayerFailureExists(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "dns-udp", Status: ProbeFail, Classification: ClassDNSUDPFailure},
		{ID: "pmtu", Status: ProbeFail, Classification: ClassLikelyPMTUBlackhole},
	}})
	if report.PrimaryClassification != ClassDNSUDPFailure {
		t.Fatalf("expected DNS root cause to win, got %q", report.PrimaryClassification)
	}
}

func TestFinalizeTreatsOneDoHProviderFailureAsDegraded(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "doh-cloudflare", Status: ProbePass},
		{ID: "doh-google", Status: ProbeFail, Classification: ClassDoHPartialFailure},
	}})
	if report.Status != StatusDegraded || report.PrimaryClassification != ClassDoHPartialFailure {
		t.Fatalf("unexpected DoH partial result: %#v", report)
	}
}

func TestFinalizeTreatsHistoricalInactiveReportAsUnavailable(t *testing.T) {
	report := Finalize(Report{Historical: true, Probes: []ProbeResult{{ID: "session", Status: ProbeFail, Classification: ClassSessionInactive}}})
	if report.Status != StatusUnavailable {
		t.Fatalf("expected unavailable historical status, got %q", report.Status)
	}
}

func TestFinalizeTreatsLiveInactiveReportAsUnavailable(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{{ID: "session", Status: ProbeFail, Classification: ClassSessionInactive}}})
	if report.Status != StatusUnavailable {
		t.Fatalf("expected unavailable live status, got %q", report.Status)
	}
}
