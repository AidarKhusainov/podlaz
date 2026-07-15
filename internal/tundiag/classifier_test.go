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
		{ID: "doh-cloudflare", Layer: LayerDoH, Status: ProbePass},
		{ID: "doh-google", Layer: LayerDoH, Status: ProbeFail, Classification: ClassDoHFailure},
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

func TestFinalizeTreatsOnePMTUTransferFailureAsDegradedEvidence(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "https-small", Layer: LayerHTTPS, Status: ProbePass},
		{ID: "pmtu-one", Layer: LayerPMTU, Status: ProbeFail, Classification: ClassHTTPSFailure, Evidence: Evidence{HTTP: &HTTPEvidence{ResponseAccepted: true, FailurePhase: "body_timeout"}}},
	}})
	if report.Status != StatusDegraded || report.PrimaryClassification != "" {
		t.Fatalf("unexpected single PMTU evidence result: %#v", report)
	}
}

func TestFinalizeRequiresTwoIndependentPMTUTransportFailures(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "https-small", Layer: LayerHTTPS, Status: ProbePass},
		{ID: "pmtu-one", Layer: LayerPMTU, Status: ProbeFail, Classification: ClassHTTPSFailure, Evidence: Evidence{HTTP: &HTTPEvidence{StatusCode: 200, ResponseAccepted: true, FailurePhase: "body_transport"}}},
		{ID: "pmtu-two", Layer: LayerPMTU, Status: ProbeFail, Classification: ClassTimeout, Evidence: Evidence{HTTP: &HTTPEvidence{StatusCode: 206, ResponseAccepted: true, FailurePhase: "body_timeout"}}},
	}})
	if report.Status != StatusUnhealthy || report.PrimaryClassification != ClassLikelyPMTUBlackhole {
		t.Fatalf("unexpected corroborated PMTU result: %#v", report)
	}
}
