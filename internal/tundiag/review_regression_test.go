package tundiag

import (
	"context"
	"testing"
	"time"
)

func TestFinalizeTreatsOneHTTPSProviderFailureAsDegraded(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "https-cloudflare-small", Layer: LayerHTTPS, Status: ProbePass},
		{ID: "https-google-small", Layer: LayerHTTPS, Status: ProbeFail, Classification: ClassHTTPSFailure},
	}})
	if report.Status != StatusDegraded || report.PrimaryClassification != ClassHTTPSPartialFailure {
		t.Fatalf("unexpected HTTPS partial result: %#v", report)
	}
	failed, _ := report.Probe("https-google-small")
	if failed.Classification != ClassHTTPSPartialFailure {
		t.Fatalf("expected partial classification, got %#v", failed)
	}
}

func TestFinalizeTreatsAllHTTPSProvidersFailingAsUnhealthy(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "https-cloudflare-small", Layer: LayerHTTPS, Status: ProbeFail, Classification: ClassHTTPSFailure},
		{ID: "https-google-small", Layer: LayerHTTPS, Status: ProbeFail, Classification: ClassHTTPSFailure},
	}})
	if report.Status != StatusUnhealthy || report.PrimaryClassification != ClassHTTPSFailure {
		t.Fatalf("unexpected HTTPS aggregate result: %#v", report)
	}
}

func TestFinalizeDoesNotInferPMTUFromGenericHTTPFailureAndTimeout(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "https-small", Layer: LayerHTTPS, Status: ProbePass},
		{ID: "pmtu-status", Layer: LayerPMTU, Status: ProbeFail, Classification: ClassHTTPSFailure, Evidence: Evidence{HTTP: &HTTPEvidence{StatusCode: 503, FailurePhase: "status"}}},
		{ID: "pmtu-request-timeout", Layer: LayerPMTU, Status: ProbeFail, Classification: ClassTimeout, Evidence: Evidence{HTTP: &HTTPEvidence{FailurePhase: "request_timeout"}}},
	}})
	if report.PrimaryClassification == ClassLikelyPMTUBlackhole {
		t.Fatalf("generic endpoint failures must not classify PMTU: %#v", report)
	}
}

func TestFinalizeRequiresTwoAcceptedBodyTransportFailuresForPMTU(t *testing.T) {
	report := Finalize(Report{Probes: []ProbeResult{
		{ID: "https-small", Layer: LayerHTTPS, Status: ProbePass},
		{ID: "pmtu-one", Layer: LayerPMTU, Status: ProbeFail, Classification: ClassTimeout, Evidence: Evidence{HTTP: &HTTPEvidence{StatusCode: 200, ResponseAccepted: true, FailurePhase: "body_timeout"}}},
		{ID: "pmtu-two", Layer: LayerPMTU, Status: ProbeFail, Classification: ClassHTTPSFailure, Evidence: Evidence{HTTP: &HTTPEvidence{StatusCode: 206, ResponseAccepted: true, FailurePhase: "body_transport"}}},
	}})
	if report.Status != StatusUnhealthy || report.PrimaryClassification != ClassLikelyPMTUBlackhole {
		t.Fatalf("unexpected PMTU corroboration: %#v", report)
	}
}

func TestRunnerContextCauseOverridesLayerClassification(t *testing.T) {
	report := Runner{}.Run(context.Background(), Report{}, []Probe{{
		Definition: ProbeDefinition{ID: "dns-udp", Layer: LayerDNS, Timeout: 10 * time.Millisecond},
		Run: func(ctx context.Context) ProbeResult {
			<-ctx.Done()
			return ProbeResult{Status: ProbeFail, Classification: ClassDNSUDPFailure, Error: "read DNS UDP response: timeout"}
		},
	}})
	probe, _ := report.Probe("dns-udp")
	if probe.Classification != ClassTimeout {
		t.Fatalf("expected stable timeout classification, got %#v", probe)
	}
}
