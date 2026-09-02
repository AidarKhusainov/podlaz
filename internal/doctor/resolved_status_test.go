package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type issue256DoctorRunner struct {
	result CommandResult
	err    error
}

func (r issue256DoctorRunner) LookPath(file string) (string, error) { return "/usr/bin/" + file, nil }
func (r issue256DoctorRunner) Run(context.Context, string, ...string) (CommandResult, error) {
	return r.result, r.err
}

func TestIssue256DoctorTreatsSupportedExitZeroResolvedMissingLinkAsClean(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(strings.ReplaceAll(ending, "\r", "CR"), func(t *testing.T) {
			raw := `Failed to resolve interface "podlaz0", ignoring: No such device` + ending
			runner := issue256DoctorRunner{result: CommandResult{
				Stderr:    strings.TrimSpace(raw),
				RawStderr: raw,
				ExitCode:  0,
			}}
			got := resolvedDNSDiagnosticLine(context.Background(), runner, "/usr/bin/resolvectl", "/usr/bin/ip", true)
			if got.severity != SeverityOK {
				t.Fatalf("expected clean resolved diagnostic, got severity=%v message=%q", got.severity, got.message)
			}
			if strings.Contains(strings.ToLower(got.message), "recover") {
				t.Fatalf("clean missing-link diagnostic must not suggest recovery: %q", got.message)
			}
		})
	}
}

func TestIssue256DoctorKeepsUnexpectedExitZeroStderrUnknown(t *testing.T) {
	raw := "warning: No such device appeared in unrelated text\n"
	runner := issue256DoctorRunner{result: CommandResult{
		Stderr:    strings.TrimSpace(raw),
		RawStderr: raw,
		ExitCode:  0,
	}}
	got := resolvedDNSDiagnosticLine(context.Background(), runner, "/usr/bin/resolvectl", "/usr/bin/ip", true)
	if got.severity != SeverityWarning {
		t.Fatalf("expected fail-closed warning, got severity=%v message=%q", got.severity, got.message)
	}
	if !strings.Contains(got.message, "unknown") {
		t.Fatalf("expected unknown diagnostic, got %q", got.message)
	}
}

func TestIssue256DoctorCommandRunnerContractAcceptsErrors(t *testing.T) {
	runner := issue256DoctorRunner{err: errors.New("boom"), result: CommandResult{ExitCode: -1}}
	got := resolvedDNSDiagnosticLine(context.Background(), runner, "/usr/bin/resolvectl", "/usr/bin/ip", false)
	if got.severity != SeverityWarning {
		t.Fatalf("expected warning for command failure, got %v", got.severity)
	}
}
