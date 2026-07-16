package daemon

import (
	"os"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestPersistTunDiagnosticReportFailureClearsPreviousReport(t *testing.T) {
	runtimeDir := t.TempDir()
	store := tundiag.Store{RuntimeDir: runtimeDir}
	if _, err := store.Save(tundiag.Report{
		Session: tundiag.Session{State: "inactive"},
		Probes:  []tundiag.ProbeResult{{ID: "previous", Status: tundiag.ProbePass}},
	}); err != nil {
		t.Fatalf("save previous report: %v", err)
	}

	probes := make([]tundiag.ProbeResult, 2000)
	for i := range probes {
		probes[i] = tundiag.ProbeResult{
			ID:             "large-probe",
			Status:         tundiag.ProbeFail,
			Classification: tundiag.ClassInternalDiagnosticError,
			Error:          strings.Repeat("x", 4096),
		}
	}

	manager := &XrayManager{RuntimeDir: runtimeDir}
	report, persisted := manager.persistTunDiagnosticReport(tundiag.Report{Probes: probes})
	if persisted || report.ReportPath != "" {
		t.Fatalf("failed persistence advertised a report: persisted=%t path=%q", persisted, report.ReportPath)
	}
	probe, ok := report.Probe("report-persistence")
	if !ok || probe.Classification != tundiag.ClassInternalDiagnosticError {
		t.Fatalf("missing internal persistence failure: %#v", report.Probes)
	}
	if _, _, err := store.Load(); err == nil || !os.IsNotExist(err) {
		t.Fatalf("previous report remained loadable after save failure: %v", err)
	}
}
