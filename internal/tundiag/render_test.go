package tundiag

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestHumanAndJSONRenderersUseSameRedactedModel(t *testing.T) {
	report := Report{
		Session: Session{State: "active", ProfileName: "office token=profile-secret"},
		Network: Network{ServerEndpoint: "vpn.example.test:443?token=query-secret"},
		Probes: []ProbeResult{{
			ID: "https", Layer: LayerHTTPS, Status: ProbeFail, Classification: ClassHTTPSFailure,
			Error:    "password=probe-secret",
			Evidence: Evidence{Commands: []CommandEvidence{{Command: "curl token=command-secret", Stdout: "123e4567-e89b-12d3-a456-426614174000"}}},
		}},
	}
	human := RenderHuman(report, true)
	var machine bytes.Buffer
	if err := WriteJSON(&machine, report); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{human, machine.String()} {
		for _, secret := range []string{"profile-secret", "query-secret", "probe-secret", "command-secret", "123e4567-e89b-12d3-a456-426614174000"} {
			if strings.Contains(output, secret) {
				t.Fatalf("output leaked %q: %s", secret, output)
			}
		}
	}
	var decoded Report
	if err := json.Unmarshal(machine.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != SchemaVersion || decoded.PrimaryClassification != ClassHTTPSFailure {
		t.Fatalf("unexpected JSON model: %#v", decoded)
	}
}

func TestRenderHumanShowsSkippedDependency(t *testing.T) {
	text := RenderHuman(Report{Probes: []ProbeResult{{ID: "tls", Layer: LayerTLS, Status: ProbeSkipped, DependencyReason: "dependency tcp-443 status is fail"}}}, false)
	if !strings.Contains(text, "SKIPPED") || !strings.Contains(text, "dependency tcp-443 status is fail") {
		t.Fatalf("missing skipped reason: %s", text)
	}
}
