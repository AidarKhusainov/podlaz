package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/AidarKhusainov/tunwarden/internal/doctor"
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
