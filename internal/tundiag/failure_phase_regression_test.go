package tundiag

import (
	"bytes"
	"encoding/json"
	"testing"
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
