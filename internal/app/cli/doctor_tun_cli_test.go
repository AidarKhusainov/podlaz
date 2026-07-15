package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestRunCLIDoctorTunRendersCompactVerboseAndJSONFromSameModel(t *testing.T) {
	report := tundiag.Finalize(tundiag.Report{
		Session: tundiag.Session{State: "active", Mode: "tun", ProfileName: "office", Interface: "podlaz0"},
		Network: tundiag.Network{TunMTU: 1500},
		Probes: []tundiag.ProbeResult{{
			ID: "route-ipv4", Layer: tundiag.LayerRoute, Status: tundiag.ProbePass, DurationMS: 3,
			Evidence: tundiag.Evidence{Route: &tundiag.RouteEvidence{Interface: "podlaz0", Table: "51820"}},
		}},
	})
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "compact", args: []string{"doctor", "--tun"}},
		{name: "verbose", args: []string{"doctor", "--tun", "--verbose"}},
		{name: "json", args: []string{"doctor", "--tun", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := runWithOptions(context.Background(), tc.args, &out, options{tunDoctor: func(context.Context) (tundiag.Report, error) { return report, nil }})
			if err != nil { t.Fatal(err) }
			if tc.name == "json" {
				var decoded tundiag.Report
				if err := json.Unmarshal(out.Bytes(), &decoded); err != nil { t.Fatal(err) }
				if decoded.SchemaVersion != 1 || decoded.Status != tundiag.StatusHealthy { t.Fatalf("unexpected JSON report: %#v", decoded) }
				return
			}
			if !strings.Contains(out.String(), "route-ipv4") { t.Fatalf("missing probe: %s", out.String()) }
			if tc.name == "compact" && strings.Contains(out.String(), "table=51820") { t.Fatalf("compact output leaked verbose evidence: %s", out.String()) }
			if tc.name == "verbose" && !strings.Contains(out.String(), "table=51820") { t.Fatalf("verbose output missing evidence: %s", out.String()) }
		})
	}
}

func TestRunCLIDoctorTunReturnsDiagnosticExitCodeForUnhealthyReport(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"doctor", "--tun"}, &out, options{tunDoctor: func(context.Context) (tundiag.Report, error) {
		return tundiag.Finalize(tundiag.Report{Probes: []tundiag.ProbeResult{{ID: "dns-udp", Layer: tundiag.LayerDNS, Status: tundiag.ProbeFail, Classification: tundiag.ClassDNSUDPFailure}}}), nil
	}})
	if ExitCode(err) != 3 { t.Fatalf("expected exit code 3, got %d: %v", ExitCode(err), err) }
}

func TestRunCLIDoctorTunRejectsInvalidFlagCombinations(t *testing.T) {
	for _, args := range [][]string{{"doctor", "--tun", "--core"}, {"doctor", "--verbose"}, {"doctor", "--tun", "--xray", "/tmp/xray"}} {
		var out bytes.Buffer
		err := run(context.Background(), args, &out)
		if ExitCode(err) != 2 { t.Fatalf("%v: expected usage exit code 2, got %d: %v", args, ExitCode(err), err) }
	}
}
