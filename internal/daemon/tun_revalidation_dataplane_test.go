package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

type revalidationDataPlaneStage string

const (
	revalidationStageDNSUDP revalidationDataPlaneStage = "dns-udp"
	revalidationStageDNSTCP revalidationDataPlaneStage = "dns-tcp"
	revalidationStageTCP443 revalidationDataPlaneStage = "tcp-443"
	revalidationStageTLS    revalidationDataPlaneStage = "tls"
	revalidationStageHTTPS  revalidationDataPlaneStage = "https"
)

type fakeRevalidationNetworkClient struct {
	failStage    revalidationDataPlaneStage
	contextStage revalidationDataPlaneStage
	calls        []revalidationDataPlaneStage
}

func (c *fakeRevalidationNetworkClient) DNSUDP(ctx context.Context, _, _ string, _ uint16) (tundiag.DNSEvidence, error) {
	c.calls = append(c.calls, revalidationStageDNSUDP)
	if err := c.stageError(ctx, revalidationStageDNSUDP); err != nil {
		return tundiag.DNSEvidence{}, err
	}
	return successfulDNSRevalidationEvidence(), nil
}

func (c *fakeRevalidationNetworkClient) DNSTCP(ctx context.Context, _, _ string, _ uint16) (tundiag.DNSEvidence, error) {
	c.calls = append(c.calls, revalidationStageDNSTCP)
	if err := c.stageError(ctx, revalidationStageDNSTCP); err != nil {
		return tundiag.DNSEvidence{}, err
	}
	return successfulDNSRevalidationEvidence(), nil
}

func (c *fakeRevalidationNetworkClient) TCP(ctx context.Context, _ string, _ uint16) (time.Duration, error) {
	c.calls = append(c.calls, revalidationStageTCP443)
	if err := c.stageError(ctx, revalidationStageTCP443); err != nil {
		return 0, err
	}
	return time.Millisecond, nil
}

func (c *fakeRevalidationNetworkClient) TLS(ctx context.Context, _ string, _ uint16) (tundiag.TLSEvidence, error) {
	c.calls = append(c.calls, revalidationStageTLS)
	if err := c.stageError(ctx, revalidationStageTLS); err != nil {
		return tundiag.TLSEvidence{}, err
	}
	return tundiag.TLSEvidence{Version: "TLS 1.3"}, nil
}

func (c *fakeRevalidationNetworkClient) HTTPS(ctx context.Context, _ tundiag.Target) (tundiag.HTTPEvidence, error) {
	c.calls = append(c.calls, revalidationStageHTTPS)
	if err := c.stageError(ctx, revalidationStageHTTPS); err != nil {
		return tundiag.HTTPEvidence{}, err
	}
	return tundiag.HTTPEvidence{StatusCode: 200}, nil
}

func (c *fakeRevalidationNetworkClient) stageError(ctx context.Context, stage revalidationDataPlaneStage) error {
	if c.failStage == stage {
		return errors.New("synthetic data-plane failure")
	}
	if c.contextStage == stage {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func successfulDNSRevalidationEvidence() tundiag.DNSEvidence {
	return tundiag.DNSEvidence{ResponseCode: tundiag.DNSRCodeSuccess, Addresses: []string{"192.0.2.80"}}
}

func revalidationDataPlanePlanForTest() planner.TunPlan {
	return planner.TunPlan{
		TunDevice: planner.TunDevicePlan{Name: "podlaz0"},
		DNS:       planner.TunDNSPlan{Servers: []string{"192.0.2.53"}},
	}
}

func TestTunRevalidationDataPlaneRequiresEveryLayer(t *testing.T) {
	for _, stage := range []revalidationDataPlaneStage{
		revalidationStageDNSTCP,
		revalidationStageTCP443,
		revalidationStageTLS,
		revalidationStageHTTPS,
	} {
		t.Run(string(stage), func(t *testing.T) {
			client := &fakeRevalidationNetworkClient{failStage: stage}
			runtime := newTunRevalidationRuntime(
				func(context.Context) (tunRevalidationObservation, error) {
					return tunRevalidationObservation{plan: revalidationDataPlanePlanForTest()}, nil
				},
				func(ctx context.Context, observation tunRevalidationObservation) error {
					err := verifyTunRevalidationDataPlane(ctx, observation.plan, client)
					if err == nil {
						return nil
					}
					return newTunRevalidationVerificationError(api.TunHealthConnectivityFailed, err)
				},
			)

			_ = runtime.Initialize(context.Background())
			health := runtime.Health()
			if health == nil || health.State == api.TunHealthVerified {
				t.Fatalf("%s failure published verified health: %#v", stage, health)
			}
			if health.Classification != api.TunHealthConnectivityFailed {
				t.Fatalf("%s failure classification=%q, want %q", stage, health.Classification, api.TunHealthConnectivityFailed)
			}
		})
	}
}

func TestTunRevalidationDataPlaneRunsRequiredLayersInOrder(t *testing.T) {
	client := &fakeRevalidationNetworkClient{}
	if err := verifyTunRevalidationDataPlane(context.Background(), revalidationDataPlanePlanForTest(), client); err != nil {
		t.Fatalf("verify layered revalidation data plane: %v", err)
	}
	want := []revalidationDataPlaneStage{
		revalidationStageDNSUDP,
		revalidationStageDNSTCP,
		revalidationStageTCP443,
		revalidationStageTLS,
		revalidationStageHTTPS,
	}
	if !reflect.DeepEqual(client.calls, want) {
		t.Fatalf("data-plane call order=%v, want %v", client.calls, want)
	}
}

func TestTunRevalidationDataPlaneCancellationPreemptsEveryLayer(t *testing.T) {
	for _, stage := range []revalidationDataPlaneStage{
		revalidationStageDNSUDP,
		revalidationStageDNSTCP,
		revalidationStageTCP443,
		revalidationStageTLS,
		revalidationStageHTTPS,
	} {
		t.Run(string(stage), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			client := &fakeRevalidationNetworkClient{contextStage: stage}
			err := verifyTunRevalidationDataPlane(ctx, revalidationDataPlanePlanForTest(), client)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s cancellation error=%v, want context.Canceled", stage, err)
			}
		})
	}
}

func TestTunRevalidationDataPlaneDeadlinePreemptsEveryLayer(t *testing.T) {
	for _, stage := range []revalidationDataPlaneStage{
		revalidationStageDNSUDP,
		revalidationStageDNSTCP,
		revalidationStageTCP443,
		revalidationStageTLS,
		revalidationStageHTTPS,
	} {
		t.Run(string(stage), func(t *testing.T) {
			ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			client := &fakeRevalidationNetworkClient{contextStage: stage}
			err := verifyTunRevalidationDataPlane(ctx, revalidationDataPlanePlanForTest(), client)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s deadline error=%v, want context.DeadlineExceeded", stage, err)
			}
		})
	}
}
