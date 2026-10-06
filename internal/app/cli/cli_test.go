package cli

import (
	"bytes"
	"context"
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
	if got, want := out.String(), "podlaz version dev\ncommit: unknown\nbuilt: unknown\n"; got != want {
		t.Fatalf("version output=%q want=%q", got, want)
	}
}

func TestRunCLIUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"unknown"}, &out)
	assertUsageError(t, err, out.String(), "unknown command")
}

func TestRunCLIPrimaryHelpHidesOperatorCommands(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"podlaz import", "podlaz connect", "podlaz status", "podlaz disconnect", "podlaz debug"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help missing %q: %q", want, got)
		}
	}
	for _, forbidden := range []string{"podlaz plan", "podlaz check", "podlaz recover", "podlaz doctor", "podlaz logs", "--handoff", "--mode"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("primary help exposed %q: %q", forbidden, got)
		}
	}
}

func TestRunCLIStatusRendersCleanLocalStatus(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"status"}, &out, options{
		profileStorePath: t.TempDir() + "/profiles.json",
		status: func(context.Context) status.Report { return cleanStatusReport() },
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if got := out.String(); got != "Status: Disconnected\n" {
		t.Fatalf("unexpected status output: %q", got)
	}
}

func TestRunCLIStatusReturnsDiagnosticExitCodeForStaleState(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"status"}, &out, options{
		profileStorePath: t.TempDir() + "/profiles.json",
		status: func(context.Context) status.Report {
			report := cleanStatusReport()
			report.Connection = "inactive (stale state detected)"
			report.Candidates = []status.Candidate{{Kind: "runtime-directory", Description: "runtime directory", Target: "/run/podlaz"}}
			return report
		},
	})
	if err == nil || ExitCode(err) != 3 {
		t.Fatalf("stale status err=%v exit=%d", err, ExitCode(err))
	}
	got := out.String()
	if !strings.Contains(got, "Status: Unknown") || strings.Contains(got, "/run/podlaz") {
		t.Fatalf("unsafe product status: %q", got)
	}
}

func TestRunCLIDebugDoctorUsesDaemonWhenAvailable(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"debug", "doctor"}, &out, options{
		daemonDoctor: func(context.Context) (doctor.Report, error) {
			return doctor.Report{Source: doctor.SourceDaemon, Checks: []doctor.Check{{Name: "daemon", Severity: doctor.SeverityOK, Message: "running"}}}, nil
		},
	})
	if err != nil {
		t.Fatalf("debug doctor: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Source: daemon") || !strings.Contains(got, "[OK] daemon: running") {
		t.Fatalf("doctor output: %q", got)
	}
}

func TestRunCLIDebugLogsParsesOptions(t *testing.T) {
	var gotOptions logs.Options
	err := runWithOptions(context.Background(), []string{"debug", "logs", "--daemon", "--since", "36h", "-f"}, &bytes.Buffer{}, options{
		logs: func(_ context.Context, _ io.Writer, opts logs.Options) error {
			gotOptions = opts
			return nil
		},
	})
	if err != nil {
		t.Fatalf("debug logs: %v", err)
	}
	if !gotOptions.Follow || gotOptions.Since != "36h" || gotOptions.Core {
		t.Fatalf("logs options: %#v", gotOptions)
	}
}

func TestRunCLIDebugRecoverExecuteNeedsNoPromptOrYesFlag(t *testing.T) {
	var out bytes.Buffer
	called := false
	err := runWithOptions(context.Background(), []string{"debug", "recover", "--execute"}, &out, options{
		recoverExecute: func(context.Context) (recovery.ExecuteResult, error) {
			called = true
			return recovery.ExecuteResult{Results: []recovery.CleanupResult{{
				Candidate: recovery.Candidate{Kind: "tun-interface", Description: "TUN interface", Target: "podlaz0"},
				Status:    "recovered",
			}}}, nil
		},
	})
	if err != nil || !called {
		t.Fatalf("debug recover err=%v called=%v", err, called)
	}
	if strings.Contains(out.String(), "[y/N]") || !strings.Contains(out.String(), "Recovered TUN interface") {
		t.Fatalf("unexpected recovery output: %q", out.String())
	}

	err = runWithOptions(context.Background(), []string{"debug", "recover", "--execute", "--yes"}, &bytes.Buffer{}, options{})
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("obsolete --yes accepted: %v", err)
	}
}

func TestRunCLIDebugRecoverReturnsDaemonUnavailableExitCode(t *testing.T) {
	err := runWithOptions(context.Background(), []string{"debug", "recover", "--execute"}, &bytes.Buffer{}, options{
		recoverExecute: func(context.Context) (recovery.ExecuteResult, error) {
			return recovery.ExecuteResult{}, fmt.Errorf("%w: daemon unavailable", client.ErrDaemonUnavailable)
		},
	})
	if err == nil || ExitCode(err) != 5 {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
}

func assertUsageError(t *testing.T, err error, stdout string, wantMessage string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected command to fail")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("expected exit code 2, got %d", got)
	}
	if !strings.Contains(err.Error(), wantMessage) {
		t.Fatalf("expected error containing %q, got %q", wantMessage, err.Error())
	}
	if stdout != "" {
		t.Fatalf("expected no stdout on usage error, got %q", stdout)
	}
}

func cleanStatusReport() status.Report {
	return status.Report{Daemon: "not running", Connection: "inactive", RuntimeDirectory: status.RuntimeDirectory{Message: "missing"}, Proxy: "inactive", TUN: "not managed in this build"}
}

func cleanDoctorReport() doctor.Report {
	return doctor.Report{Source: doctor.SourceLocalFallback, Checks: []doctor.Check{{Name: "platform", Severity: doctor.SeverityOK, Message: "linux/amd64"}}}
}
