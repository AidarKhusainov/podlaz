package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/doctor"
	"github.com/AidarKhusainov/tunwarden/internal/recovery"
	"github.com/AidarKhusainov/tunwarden/internal/status"
)

func TestRunCLIVersion(t *testing.T) {
	var out bytes.Buffer

	err := run(context.Background(), []string{"version"}, &out)
	if err != nil {
		t.Fatalf("version failed: %v", err)
	}

	if got := out.String(); !strings.Contains(got, "tunwarden") {
		t.Fatalf("expected version output to contain binary name, got %q", got)
	}
}

func TestRunCLIUnknownCommand(t *testing.T) {
	var out bytes.Buffer

	err := run(context.Background(), []string{"unknown"}, &out)
	if err == nil {
		t.Fatal("expected unknown command to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected unknown command exit code 2, got %d", got)
	}
}

func TestExitCodeNil(t *testing.T) {
	if got := ExitCode(nil); got != 0 {
		t.Fatalf("expected nil error exit code 0, got %d", got)
	}
}

func TestRunCLIStatusHelp(t *testing.T) {
	var out bytes.Buffer

	err := run(context.Background(), []string{"status", "--help"}, &out)
	if err != nil {
		t.Fatalf("status --help failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Usage:\n  tunwarden status") {
		t.Fatalf("expected status help output, got %q", got)
	}
}

func TestRunCLIHelpStatus(t *testing.T) {
	var out bytes.Buffer

	err := run(context.Background(), []string{"help", "status"}, &out)
	if err != nil {
		t.Fatalf("help status failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Report local TunWarden runtime state") {
		t.Fatalf("expected status help output, got %q", got)
	}
}

func TestRunCLIStatusRendersCleanLocalStatus(t *testing.T) {
	var out bytes.Buffer

	err := runWithOptions(context.Background(), []string{"status"}, &out, options{
		status: func(context.Context) status.Report {
			return cleanStatusReport()
		},
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	got := out.String()
	want := []string{
		"TunWarden status",
		"Daemon: not running",
		"Connection: inactive",
		"Runtime directory: missing",
		"Proxy: inactive",
		"TUN: not managed in this build",
		"Stale state: none",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
}

func TestRunCLIStatusReturnsDiagnosticExitCodeForStaleState(t *testing.T) {
	var out bytes.Buffer

	err := runWithOptions(context.Background(), []string{"status"}, &out, options{
		status: func(context.Context) status.Report {
			report := cleanStatusReport()
			report.Connection = "inactive (stale state detected)"
			report.RuntimeDirectory = status.RuntimeDirectory{Message: "present (stale)"}
			report.Candidates = []status.Candidate{{
				Kind:        "runtime-directory",
				Description: "runtime directory",
				Target:      "/run/tunwarden",
			}}
			return report
		},
	})

	if err == nil {
		t.Fatal("expected stale status to return diagnostic exit code")
	}
	if got := ExitCode(err); got != 3 {
		t.Fatalf("expected status diagnostic exit code 3, got %d", got)
	}
	if got := out.String(); !strings.Contains(got, "Guidance: run `tunwarden recover`") {
		t.Fatalf("expected recovery guidance in status output, got %q", got)
	}
}

func TestRunCLIStatusRejectsUnsupportedArguments(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{
			name:        "json",
			args:        []string{"status", "--json"},
			wantMessage: "status --json is not implemented yet",
		},
		{
			name:        "garbage",
			args:        []string{"status", "garbage"},
			wantMessage: "unsupported status argument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer

			err := run(context.Background(), tt.args, &out)
			if err == nil {
				t.Fatalf("expected %v to fail", tt.args)
			}
			if got := ExitCode(err); got != 2 {
				t.Fatalf("expected exit code 2, got %d", got)
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected error containing %q, got %q", tt.wantMessage, err.Error())
			}
			if got := out.String(); got != "" {
				t.Fatalf("expected no stdout on usage error, got %q", got)
			}
		})
	}
}

func TestRunCLIDoctorHelp(t *testing.T) {
	var out bytes.Buffer

	err := run(context.Background(), []string{"doctor", "--help"}, &out)
	if err != nil {
		t.Fatalf("doctor --help failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Usage:\n  tunwarden doctor") {
		t.Fatalf("expected doctor help output, got %q", got)
	}
}

func TestRunCLIHelpDoctor(t *testing.T) {
	var out bytes.Buffer

	err := run(context.Background(), []string{"help", "doctor"}, &out)
	if err != nil {
		t.Fatalf("help doctor failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Run read-only local diagnostics") {
		t.Fatalf("expected doctor help output, got %q", got)
	}
}

func TestRunCLIDoctorRejectsUnsupportedArguments(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{
			name:        "json",
			args:        []string{"doctor", "--json"},
			wantMessage: "doctor --json is not implemented yet",
		},
		{
			name:        "core",
			args:        []string{"doctor", "--core"},
			wantMessage: "doctor --core is not implemented yet",
		},
		{
			name:        "garbage",
			args:        []string{"doctor", "garbage"},
			wantMessage: "unsupported doctor argument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer

			err := run(context.Background(), tt.args, &out)
			if err == nil {
				t.Fatalf("expected %v to fail", tt.args)
			}
			if got := ExitCode(err); got != 2 {
				t.Fatalf("expected exit code 2, got %d", got)
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected error containing %q, got %q", tt.wantMessage, err.Error())
			}
			if got := out.String(); got != "" {
				t.Fatalf("expected no stdout on usage error, got %q", got)
			}
		})
	}
}

func TestRunCLIVersionRejectsArguments(t *testing.T) {
	var out bytes.Buffer

	err := run(context.Background(), []string{"version", "garbage"}, &out)
	if err == nil {
		t.Fatal("expected version garbage to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
}

func TestRunCLIRecoverRendersDryRunPlan(t *testing.T) {
	var out bytes.Buffer

	err := runWithOptions(context.Background(), []string{"recover"}, &out, options{
		recover: func(context.Context) recovery.PlanResult {
			return recovery.PlanResult{Candidates: []recovery.Candidate{
				{Kind: "tun-interface", Description: "TUN interface", Target: "tunwarden0"},
			}}
		},
	})
	if err != nil {
		t.Fatalf("recover failed: %v", err)
	}

	got := out.String()
	want := []string{
		"TunWarden recovery dry-run",
		"Would recover TUN interface: tunwarden0",
		"No changes were applied.",
	}
	for _, text := range want {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
}

func TestRunCLIRecoverRejectsExecute(t *testing.T) {
	var out bytes.Buffer

	err := run(context.Background(), []string{"recover", "--execute", "--yes"}, &out)
	if err == nil {
		t.Fatal("expected recover --execute --yes to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
	if !strings.Contains(err.Error(), "recover --execute is not implemented in v0.1") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunCLIDoctorReturnsDiagnosticExitCodeForFailures(t *testing.T) {
	var out bytes.Buffer

	err := runWithOptions(context.Background(), []string{"doctor"}, &out, options{
		doctor: func(context.Context) doctor.Report {
			return doctor.Report{Checks: []doctor.Check{{
				Name:     "default-route",
				Severity: doctor.SeverityFail,
				Message:  "ip route show default failed: test failure",
			}}}
		},
	})

	if err == nil {
		t.Fatal("expected doctor to fail when report has failing checks")
	}
	if got := ExitCode(err); got != 3 {
		t.Fatalf("expected doctor diagnostic exit code 3, got %d", got)
	}
	if got := out.String(); !strings.Contains(got, "[FAIL] default-route") {
		t.Fatalf("expected failing diagnostic in output, got %q", got)
	}
}

func cleanStatusReport() status.Report {
	return status.Report{
		Daemon:           "not running",
		Connection:       "inactive",
		RuntimeDirectory: status.RuntimeDirectory{Message: "missing"},
		Proxy:            "inactive",
		TUN:              "not managed in this build",
	}
}
