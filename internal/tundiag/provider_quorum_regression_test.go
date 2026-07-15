package tundiag

import "testing"

func TestFinalizePreservesHTTPSProviderTimeoutBehindPartialAggregate(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "https-cloudflare-small", Layer: LayerHTTPS, Status: ProbePass},
		{ID: "https-google-small", Layer: LayerHTTPS, Status: ProbeFail, Classification: ClassTimeout},
	}})
	if report.Status != StatusDegraded || report.PrimaryClassification != ClassHTTPSPartialFailure {
		t.Fatalf("expected degraded HTTPS quorum, got %#v", report)
	}
	failed, _ := report.Probe("https-google-small")
	if failed.Classification != ClassTimeout {
		t.Fatalf("provider root classification was overwritten: %#v", failed)
	}
}

func TestFinalizeDoesNotSuppressCancelledHTTPSProviderProbe(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "https-cloudflare-small", Layer: LayerHTTPS, Status: ProbePass},
		{ID: "https-google-small", Layer: LayerHTTPS, Status: ProbeFail, Classification: ClassCancelled},
	}})
	if report.Status != StatusUnhealthy || report.PrimaryClassification != ClassCancelled {
		t.Fatalf("cancelled diagnostic must remain unhealthy and machine-readable: %#v", report)
	}
	failed, _ := report.Probe("https-google-small")
	if failed.Classification != ClassCancelled {
		t.Fatalf("cancelled classification was overwritten: %#v", failed)
	}
}

func TestFinalizeDoesNotSuppressInternalHTTPSProviderProbe(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "https-cloudflare-small", Layer: LayerHTTPS, Status: ProbePass},
		{ID: "https-google-small", Layer: LayerHTTPS, Status: ProbeFail, Classification: ClassInternalDiagnosticError},
	}})
	if report.Status != StatusUnhealthy || report.PrimaryClassification != ClassInternalDiagnosticError {
		t.Fatalf("internal diagnostic error must not become provider degradation: %#v", report)
	}
}

func TestFinalizePreservesDoHProviderTimeoutBehindPartialAggregate(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "doh-cloudflare", Layer: LayerDoH, Status: ProbePass},
		{ID: "doh-google", Layer: LayerDoH, Status: ProbeFail, Classification: ClassTimeout},
	}})
	if report.Status != StatusDegraded || report.PrimaryClassification != ClassDoHPartialFailure {
		t.Fatalf("expected degraded DoH quorum, got %#v", report)
	}
	failed, _ := report.Probe("doh-google")
	if failed.Classification != ClassTimeout {
		t.Fatalf("DoH root classification was overwritten: %#v", failed)
	}
}

func TestFinalizeTreatsCloudflareTCPFailureAsPartialWhenGoogleHTTPSPasses(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "tcp-443", Layer: LayerTCP, Status: ProbeFail, Classification: ClassTCP443Failure},
		{ID: "https-google-small", Layer: LayerHTTPS, Status: ProbePass},
	}})
	if report.Status != StatusDegraded || report.PrimaryClassification != ClassHTTPSPartialFailure {
		t.Fatalf("single Cloudflare TCP outage must be degraded when Google HTTPS passes: %#v", report)
	}
	failed, _ := report.Probe("tcp-443")
	if failed.Classification != ClassTCP443Failure {
		t.Fatalf("TCP root classification was overwritten: %#v", failed)
	}
}

func TestFinalizeTreatsCloudflareTLSFailureAsPartialWhenGoogleHTTPSPasses(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "tls", Layer: LayerTLS, Status: ProbeFail, Classification: ClassTLSFailure},
		{ID: "https-google-small", Layer: LayerHTTPS, Status: ProbePass},
	}})
	if report.Status != StatusDegraded || report.PrimaryClassification != ClassHTTPSPartialFailure {
		t.Fatalf("single Cloudflare TLS outage must be degraded when Google HTTPS passes: %#v", report)
	}
	failed, _ := report.Probe("tls")
	if failed.Classification != ClassTLSFailure {
		t.Fatalf("TLS root classification was overwritten: %#v", failed)
	}
}
