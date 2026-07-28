package tundiag

import (
	"context"
	"testing"
	"time"
)

type fakeClock struct{ values []time.Time }

func (c *fakeClock) Now() time.Time {
	value := c.values[0]
	c.values = c.values[1:]
	return value
}

func TestRunnerSkipsDependentProbesAfterRouteFailure(t *testing.T) {
	start := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{values: []time.Time{start, start, start.Add(20 * time.Millisecond)}}
	report := Runner{Clock: clock}.Run(context.Background(), Report{}, []Probe{
		{
			Definition: ProbeDefinition{ID: "route-ipv4", Layer: LayerRoute, Timeout: time.Second},
			Run: func(context.Context) ProbeResult {
				return ProbeResult{Status: ProbeFail, Classification: ClassRouteFailure, Error: "wrong interface"}
			},
		},
		{
			Definition: ProbeDefinition{ID: "dns-udp", Layer: LayerDNS, Timeout: time.Second, DependsOn: []string{"route-ipv4"}},
			Run: func(context.Context) ProbeResult {
				t.Fatal("dependent probe must not run")
				return ProbeResult{}
			},
		},
	})

	if report.Status != StatusUnhealthy || report.PrimaryClassification != ClassRouteFailure {
		t.Fatalf("unexpected report classification: %#v", report)
	}
	if got := report.Probes[1]; got.Status != ProbeSkipped || got.DependencyReason != privacyDiagnosticText {
		t.Fatalf("unexpected skipped result: %#v", got)
	}
}

func TestRunnerClassifiesDeadlineAndStopsNewWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := Runner{}.Run(ctx, Report{}, []Probe{{
		Definition: ProbeDefinition{ID: "route-ipv4", Layer: LayerRoute, Timeout: time.Second},
		Run: func(context.Context) ProbeResult {
			t.Fatal("cancelled run must not start probes")
			return ProbeResult{}
		},
	}})
	if got := report.Probes[0]; got.Status != ProbeSkipped || got.Classification != ClassCancelled {
		t.Fatalf("unexpected cancellation result: %#v", got)
	}
	if report.PrimaryClassification != ClassCancelled || report.Status != StatusUnhealthy {
		t.Fatalf("unexpected cancellation report: %#v", report)
	}
}

func TestRunnerConvertsProbePanicToInternalDiagnosticFailure(t *testing.T) {
	report := Runner{}.Run(context.Background(), Report{}, []Probe{{
		Definition: ProbeDefinition{ID: "tls", Layer: LayerTLS, Timeout: time.Second},
		Run:        func(context.Context) ProbeResult { panic("boom token=secret") },
	}})
	got := report.Probes[0]
	if got.Status != ProbeFail || got.Classification != ClassInternalDiagnosticError {
		t.Fatalf("unexpected panic result: %#v", got)
	}
	if got.Error != privacyDiagnosticText {
		t.Fatalf("panic detail crossed public privacy boundary: %q", got.Error)
	}
}
