package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

type issue262ProbeClient struct {
	httpsTargets []string
	dnsUDPErr    error
	dnsTCPCalls  int
}

func (c *issue262ProbeClient) DNSUDP(context.Context, string, string, uint16) (tundiag.DNSEvidence, error) {
	if c.dnsUDPErr != nil {
		return tundiag.DNSEvidence{}, c.dnsUDPErr
	}
	return successfulDNSRevalidationEvidence(), nil
}

func (c *issue262ProbeClient) DNSTCP(context.Context, string, string, uint16) (tundiag.DNSEvidence, error) {
	c.dnsTCPCalls++
	return successfulDNSRevalidationEvidence(), nil
}

func (c *issue262ProbeClient) TCP(context.Context, string, uint16) (time.Duration, error) {
	return time.Millisecond, nil
}

func (c *issue262ProbeClient) TLS(context.Context, string, uint16) (tundiag.TLSEvidence, error) {
	return tundiag.TLSEvidence{Version: "TLS 1.3"}, nil
}

func (c *issue262ProbeClient) HTTPS(_ context.Context, target tundiag.Target) (tundiag.HTTPEvidence, error) {
	c.httpsTargets = append(c.httpsTargets, target.ID)
	if target.ID == "https-cloudflare-small" {
		return tundiag.HTTPEvidence{}, errors.New("synthetic Cloudflare endpoint failure")
	}
	return tundiag.HTTPEvidence{StatusCode: 204}, nil
}

func TestIssue262ProbeEvidenceContinuesAfterOneProviderFailure(t *testing.T) {
	client := &issue262ProbeClient{}
	evidence, err := collectTunRevalidationProbeEvidence(context.Background(), revalidationDataPlanePlanForTest(), client)
	if err != nil {
		t.Fatalf("collect probe evidence: %v", err)
	}

	var cloudflareFailed, googleSucceeded bool
	for _, item := range evidence {
		if item.Group != "https" {
			continue
		}
		switch item.Provider {
		case "cloudflare":
			cloudflareFailed = !item.Success && item.Cause != nil
		case "google":
			googleSucceeded = item.Success && item.Cause == nil
		}
	}
	if !cloudflareFailed || !googleSucceeded {
		t.Fatalf("provider evidence=%#v, want Cloudflare failure plus independent Google success", evidence)
	}
}

func TestIssue262ProbeEvidenceContinuesAfterOneProbeDeadlineExceeded(t *testing.T) {
	client := &issue262ProbeClient{dnsUDPErr: context.DeadlineExceeded}
	evidence, err := collectTunRevalidationProbeEvidence(context.Background(), revalidationDataPlanePlanForTest(), client)
	if err != nil {
		t.Fatalf("one probe timeout aborted independent sampling: %v", err)
	}
	if client.dnsTCPCalls == 0 {
		t.Fatal("DNS TCP probe did not run after DNS UDP timeout")
	}

	var timedOutUDP, googleSucceeded bool
	for _, item := range evidence {
		switch {
		case item.Group == "dns-udp" && item.Provider == "session-resolver":
			timedOutUDP = !item.Success && errors.Is(item.Cause, context.DeadlineExceeded)
		case item.Group == "https" && item.Provider == "google":
			googleSucceeded = item.Success && item.Cause == nil
		}
	}
	if !timedOutUDP || !googleSucceeded {
		t.Fatalf("probe evidence=%#v, want soft DNS UDP timeout plus later independent success", evidence)
	}
}

func TestIssue262ProbeEvidenceDoesNotCountCloudflareStagesAsIndependentProviders(t *testing.T) {
	client := &issue262ProbeClient{}
	evidence, err := collectTunRevalidationProbeEvidence(context.Background(), revalidationDataPlanePlanForTest(), client)
	if err != nil {
		t.Fatalf("collect probe evidence: %v", err)
	}

	providers := map[string]struct{}{}
	for _, item := range evidence {
		if item.Provider == "cloudflare" {
			providers[item.Provider] = struct{}{}
		}
	}
	if len(providers) != 1 {
		t.Fatalf("Cloudflare stages produced %d provider identities, want exactly one", len(providers))
	}
}
