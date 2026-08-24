package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/client"
	"github.com/AidarKhusainov/podlaz/internal/doctor"
	"github.com/AidarKhusainov/podlaz/internal/logs"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	"github.com/AidarKhusainov/podlaz/internal/status"
)

func TestRunCLIVersion(t *testing.T) {
	oldVersion, oldCommit, oldBuilt := version, commit, built
	t.Cleanup(func() { version, commit, built = oldVersion, oldCommit, oldBuilt })
	version, commit, built = "", "", ""

	var out bytes.Buffer
	if err := run(context.Background(), []string{"version"}, &out); err != nil {
		t.Fatalf("version failed: %v", err)
	}
	want := "podlaz version dev\ncommit: unknown\nbuilt: unknown\n"
	if got := out.String(); got != want {
		t.Fatalf("expected version output %q, got %q", want, got)
	}
}

func TestExitCodeNil(t *testing.T) {
	if got := ExitCode(nil); got != 0 {
		t.Fatalf("expected nil error exit code 0, got %d", got)
	}
}

func TestRunCLIUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"unknown"}, &out)
	assertUsageError(t, err, out.String(), "unknown command")
}

func TestRunCLIStatusHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"status", "--help"}, &out); err != nil {
		t.Fatalf("status --help failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Usage:\n  podlaz status") {
		t.Fatalf("expected status help output, got %q", got)
	}
}

func TestRunCLIStatusRendersCleanLocalStatus(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"status"}, &out, options{
		status: func(context.Context) status.Report { return cleanStatusReport() },
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if got := out.String(); got != "Status: Disconnected\n" {
		t.Fatalf("unexpected concise status output: %q", got)
	}
}

func TestRunCLIStatusReturnsDiagnosticExitCodeForStaleState(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"status"}, &out, options{
		status: func(context.Context) status.Report {
			report := cleanStatusReport()
			report.Connection = "inactive (stale state detected)"
			report.RuntimeDirectory = status.RuntimeDirectory{Message: "present (stale)"}
			report.Candidates = []status.Candidate{{Kind: "runtime-directory", Description: "runtime directory", Target: "/run/podlaz"}}
			return report
		},
	})
	if err == nil {
		t.Fatal("expected stale status to return diagnostic exit code")
	}
	if got := ExitCode(err); got != 3 {
		t.Fatalf("expected status diagnostic exit code 3, got %d", got)
	}
	got := out.String()
	if !strings.Contains(got, "Status: Unknown") || !strings.Contains(got, "Reason: Connection state could not be determined") {
		t.Fatalf("expected safe product status, got %q", got)
	}
	if strings.Contains(got, "Recovery candidates:") || strings.Contains(got, "/run/podlaz") {
		t.Fatalf("primary status leaked operator detail: %q", got)
	}
}

func TestRunCLIStatusRejectsUnsupportedArguments(t *testing.T) {
	for _, tt := range []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{name: "json", args: []string{"status", "--json"}, wantMessage: "status --json is not implemented yet"},
		{name: "garbage", args: []string{"status", "garbage"}, wantMessage: "unsupported status argument"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(context.Background(), tt.args, &out)
			assertUsageError(t, err, out.String(), tt.wantMessage)
		})
	}
}

func TestRunCLIDoctorUsesDaemonWhenAvailable(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"doctor"}, &out, options{
		daemonDoctor: func(context.Context) (doctor.Report, error) {
			return doctor.Report{Source: doctor.SourceDaemon, Checks: []doctor.Check{{Name: "daemon", Severity: doctor.SeverityOK, Message: "running"}}}, nil
		},
	})
	if err != nil {
		t.Fatalf("doctor failed: %v", err)
	}
	if !strings.Contains(out.String(), "daemon") {
		t.Fatalf("expected doctor output, got %q", out.String())
	}
}

func TestRunCLIDoctorFallsBackWhenDaemonUnavailable(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"doctor"}, &out, options{
		daemonDoctor: func(context.Context) (doctor.Report, error) { return doctor.Report{}, client.ErrDaemonUnavailable },
		doctor: func(context.Context) doctor.Report {
			return doctor.Report{Source: doctor.SourceLocal, Checks: []doctor.Check{{Name: "local", Severity: doctor.SeverityOK, Message: "ok"}}}
		},
	})
	if err != nil {
		t.Fatalf("doctor fallback failed: %v", err)
	}
}

func TestRunCLILogs(t *testing.T) {
	var out bytes.Buffer
	called := false
	err := runWithOptions(context.Background(), []string{"logs"}, &out, options{
		logs: func(_ context.Context, w io.Writer, _ logs.Options) error {
			called = true
			_, _ = fmt.Fprintln(w, "example log")
			return nil
		},
	})
	if err != nil || !called {
		t.Fatalf("logs err=%v called=%v", err, called)
	}
}

func TestRunCLIRecover(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"recover"}, &out, options{
		recover: func(context.Context) recovery.PlanResult { return recovery.PlanResult{} },
	})
	if err != nil {
		t.Fatalf("recover failed: %v", err)
	}
}

func assertUsageError(t *testing.T, err error, output, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected usage error containing %q", want)
	}
	if ExitCode(err) != 2 {
		t.Fatalf("expected exit code 2, got %d: %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), want) && !strings.Contains(output, want) {
		t.Fatalf("expected %q in error/output, err=%v output=%q", want, err, output)
	}
}

var _ = errors.New
