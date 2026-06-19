package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/doctor"
)

func TestRunCLIDoctorCoreRendersXrayDiagnostics(t *testing.T) {
	var out bytes.Buffer
	var gotPath string

	err := runWithOptions(context.Background(), []string{"doctor", "--core", "--xray", "/usr/local/bin/xray"}, &out, options{
		coreDoctor: func(_ context.Context, xrayPath string) doctor.Report {
			gotPath = xrayPath
			return cleanCoreDoctorReport(xrayPath)
		},
	})
	if err != nil {
		t.Fatalf("doctor --core failed: %v", err)
	}
	if gotPath != "/usr/local/bin/xray" {
		t.Fatalf("expected xray path to be passed through, got %q", gotPath)
	}
	got := out.String()
	for _, text := range []string{"podlaz core diagnostics", "[OK] xray: /usr/local/bin/xray is executable", "[OK] xray-version: Xray 25.6.1", "[WARN] config-test: not checked"} {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
}

func TestRunCLIDoctorCoreSupportsInlineXrayPathAndJSON(t *testing.T) {
	var out bytes.Buffer

	err := runWithOptions(context.Background(), []string{"doctor", "--core", "--xray=/usr/local/bin/xray", "--json"}, &out, options{
		coreDoctor: func(_ context.Context, xrayPath string) doctor.Report {
			return cleanCoreDoctorReport(xrayPath)
		},
	})
	if err != nil {
		t.Fatalf("doctor --core --json failed: %v", err)
	}

	var response doctorJSONResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("doctor --core --json must emit valid JSON: %v\n%s", err, out.String())
	}
	if response.SchemaVersion != "v1" {
		t.Fatalf("expected schema_version v1, got %q", response.SchemaVersion)
	}
	if response.Status != "warn" {
		t.Fatalf("expected warning status because config-test is deferred, got %q", response.Status)
	}
	if len(response.Warnings) != 1 || response.Warnings[0] != "not checked" {
		t.Fatalf("expected config-test warning in top-level warnings, got %#v", response.Warnings)
	}
	if len(response.Errors) != 0 {
		t.Fatalf("expected no top-level errors, got %#v", response.Errors)
	}
	if response.Source != doctor.SourceLocalCore {
		t.Fatalf("expected local core source, got %q", response.Source)
	}
	if len(response.Checks) != 3 {
		t.Fatalf("expected three checks, got %#v", response.Checks)
	}
	if response.Checks[1].Name != "xray-version" || response.Checks[1].Severity != string(doctor.SeverityOK) {
		t.Fatalf("expected xray-version OK check, got %#v", response.Checks[1])
	}
}

func TestRunCLIDoctorCoreRedactsHumanAndJSONOutput(t *testing.T) {
	uuid := "123e4567-e89b-12d3-a456-426614174000"
	secretReport := doctor.Report{Source: doctor.SourceLocalCore, Checks: []doctor.Check{
		{Name: "xray", Severity: doctor.SeverityOK, Message: "/usr/local/bin/xray token=xray-token " + uuid},
		{Name: "xray-version", Severity: doctor.SeverityOK, Message: "Xray 25.6.1 password=xray-password"},
		{Name: "config-test", Severity: doctor.SeverityWarning, Message: "not checked token=config-token password=config-password " + uuid},
	}}

	var human bytes.Buffer
	err := runWithOptions(context.Background(), []string{"doctor", "--core", "--xray", "/usr/local/bin/xray"}, &human, options{
		coreDoctor: func(context.Context, string) doctor.Report { return secretReport },
	})
	if err != nil {
		t.Fatalf("doctor --core human output failed: %v", err)
	}

	var machine bytes.Buffer
	err = runWithOptions(context.Background(), []string{"doctor", "--core", "--xray", "/usr/local/bin/xray", "--json"}, &machine, options{
		coreDoctor: func(context.Context, string) doctor.Report { return secretReport },
	})
	if err != nil {
		t.Fatalf("doctor --core JSON output failed: %v", err)
	}

	for _, output := range []string{human.String(), machine.String()} {
		assertNoDoctorCoreSecretLeak(t, output, []string{"xray-token", "xray-password", "config-token", "config-password", uuid})
		for _, want := range []string{"token=REDACTED", "password=REDACTED", "123e…4000"} {
			if !strings.Contains(output, want) {
				t.Fatalf("expected output to contain redacted marker %q, got %q", want, output)
			}
		}
	}

	var response doctorJSONResponse
	if err := json.Unmarshal(machine.Bytes(), &response); err != nil {
		t.Fatalf("doctor --core --json must emit valid JSON: %v\n%s", err, machine.String())
	}
	if response.Warnings[0] != "not checked token=REDACTED password=REDACTED 123e…4000" {
		t.Fatalf("expected redacted warning, got %#v", response.Warnings)
	}
}

func TestRunCLIDoctorCoreReturnsDiagnosticExitCodeForFailures(t *testing.T) {
	var out bytes.Buffer

	err := runWithOptions(context.Background(), []string{"doctor", "--core", "--xray", "/missing/xray"}, &out, options{
		coreDoctor: func(_ context.Context, xrayPath string) doctor.Report {
			return doctor.Report{Source: doctor.SourceLocalCore, Checks: []doctor.Check{{Name: "xray", Severity: doctor.SeverityFail, Message: xrayPath + " does not exist; install Xray or pass the correct --xray path"}}}
		},
	})
	if err == nil {
		t.Fatal("expected doctor --core to fail when xray check fails")
	}
	if got := ExitCode(err); got != 3 {
		t.Fatalf("expected doctor --core diagnostic exit code 3, got %d", got)
	}
	if got := out.String(); !strings.Contains(got, "[FAIL] xray") || !strings.Contains(got, "does not exist") {
		t.Fatalf("expected failing xray diagnostic in output, got %q", got)
	}
}

func TestRunCLIDoctorCoreRejectsMissingXrayValue(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"doctor", "--core", "--xray"}, &out)
	if err == nil {
		t.Fatal("expected missing --xray value to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
	if !strings.Contains(err.Error(), "doctor --core --xray requires a value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func cleanCoreDoctorReport(xrayPath string) doctor.Report {
	return doctor.Report{Source: doctor.SourceLocalCore, Checks: []doctor.Check{
		{Name: "xray", Severity: doctor.SeverityOK, Message: xrayPath + " is executable"},
		{Name: "xray-version", Severity: doctor.SeverityOK, Message: "Xray 25.6.1 (Xray, Penetrates Everything.)"},
		{Name: "config-test", Severity: doctor.SeverityWarning, Message: "not checked"},
	}}
}

func assertNoDoctorCoreSecretLeak(t *testing.T, output string, leaked []string) {
	t.Helper()
	for _, value := range leaked {
		if strings.Contains(output, value) {
			t.Fatalf("doctor --core output leaked %q in %q", value, output)
		}
	}
}
