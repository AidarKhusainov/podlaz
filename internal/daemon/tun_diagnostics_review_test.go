package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestBuildHardenedTunDiagnosticAdaptersValidatesServerBypassMainPath(t *testing.T) {
	input := tunDiagnosticInput{plan: planner.TunPlan{ServerBypass: planner.TunRoutePlan{
		Destination: "203.0.113.10/32",
		Table:       planner.MainRoutingTable,
		Interface:   "eth0",
		Gateway:     "192.0.2.1",
	}}}
	original := tunDiagnosticCommandRunner
	tunDiagnosticCommandRunner = func(_ context.Context, name string, args ...string) (tunDiagnosticCommandResult, error) {
		command := strings.TrimSpace(name + " " + strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "-4 route get 203.0.113.10":
			return tunDiagnosticCommandResult{command: command, stdout: "203.0.113.10 via 192.0.2.254 dev eth0 table main\n"}, nil
		case "-4 rule show":
			return tunDiagnosticCommandResult{command: command, stdout: "9999: to 203.0.113.10 lookup main\n"}, nil
		default:
			return tunDiagnosticCommandResult{command: command, exitCode: 1}, errors.New("unexpected command")
		}
	}
	t.Cleanup(func() { tunDiagnosticCommandRunner = original })

	result := buildHardenedTunDiagnosticAdapters(input).ServerBypass(context.Background())
	if result.Status != tundiag.ProbeFail || result.Classification != tundiag.ClassServerBypassFailure || !strings.Contains(result.Error, "gateway") {
		t.Fatalf("expected gateway mismatch through production adapter construction, got %#v", result)
	}
}

func TestPersistTunDiagnosticReportDoesNotAdvertiseMissingFile(t *testing.T) {
	runtimePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(runtimePath, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := &XrayManager{RuntimeDir: runtimePath}
	report, persisted := manager.persistTunDiagnosticReport(tundiag.Report{})
	if persisted || report.ReportPath != "" {
		t.Fatalf("failed persistence must not advertise a report path: persisted=%t report=%#v", persisted, report)
	}
	probe, ok := report.Probe("report-persistence")
	if !ok || probe.Classification != tundiag.ClassInternalDiagnosticError {
		t.Fatalf("expected internal persistence classification, got %#v", report.Probes)
	}
	summary := tunFailureDiagnosticSummary{PrimaryClassification: report.PrimaryClassification, ReportPath: report.ReportPath, Persisted: persisted}
	if strings.Contains(summary.String(), tundiag.LastReportFileName) || !strings.Contains(summary.String(), "unavailable") {
		t.Fatalf("unexpected failed-persistence summary %q", summary.String())
	}
}

func TestTunTransactionDiagnosticLabelsPreserveHostnameAndSNI(t *testing.T) {
	labels := tunTransactionDiagnosticLabels(profile.Profile{Server: "vpn.example.test", Port: 443, ServerName: "edge.example.test"})
	if got := labels[tunTransactionLabelServerEndpoint]; got != "vpn.example.test:443" {
		t.Fatalf("unexpected endpoint label %q", got)
	}
	if got := labels[tunTransactionLabelServerName]; got != "edge.example.test" {
		t.Fatalf("unexpected SNI label %q", got)
	}
}

func TestParseTunDiagnosticGlobalIPv6AddressesExcludesLinkLocalAndInvalidTokens(t *testing.T) {
	uplink, tun := parseTunDiagnosticGlobalIPv6Addresses(
		"eth0 UP fe80::1/64 2001:db8:1::2/64 invalid:token\npodlaz0 UNKNOWN ::1/128 2001:db8:2::2/64\n",
		"eth0", "podlaz0",
	)
	if got := strings.Join(uplink, ","); got != "2001:db8:1::2/64" {
		t.Fatalf("unexpected uplink addresses %q", got)
	}
	if got := strings.Join(tun, ","); got != "2001:db8:2::2/64" {
		t.Fatalf("unexpected TUN addresses %q", got)
	}
}
