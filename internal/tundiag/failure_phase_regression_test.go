package tundiag

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestProbeFailurePhaseRoundTripsJSON(t *testing.T) {
	payload := []byte(`{
		"schema_version":1,
		"session":{"state":"active","core_running":true},
		"network":{},
		"probes":[{
			"id":"tcp-443",
			"layer":"tcp",
			"status":"fail",
			"duration_ms":1,
			"timeout_ms":4000,
			"classification":"timeout",
			"failure_phase":"route_lookup"
		}],
		"warnings":[],
		"errors":[]
	}`)
	var report Report
	if err := json.Unmarshal(payload, &report); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"failure_phase":"route_lookup"`)) {
		t.Fatalf("failure phase was dropped from the stable JSON model: %s", encoded)
	}
}

func TestRunnerPreservesFailurePhaseWhenDeadlineOverridesClassification(t *testing.T) {
	report := Runner{}.Run(context.Background(), Report{}, []Probe{{
		Definition: ProbeDefinition{ID: "tcp-443", Layer: LayerTCP, Timeout: 10 * time.Millisecond},
		Run: func(ctx context.Context) ProbeResult {
			<-ctx.Done()
			return ProbeResult{
				Status:         ProbeFail,
				Classification: ClassRouteFailure,
				FailurePhase:   FailurePhaseRouteLookup,
				Error:          "route command timed out",
			}
		},
	}})
	probe, ok := report.Probe("tcp-443")
	if !ok {
		t.Fatal("missing tcp-443 probe")
	}
	if probe.Classification != ClassTimeout || probe.FailurePhase != FailurePhaseRouteLookup {
		t.Fatalf("Runner must preserve phase while applying timeout classification: %#v", probe)
	}
}
