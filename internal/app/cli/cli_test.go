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
	got := out.String()
	for _, text := range []string{"podlaz status", "Daemon: not running", "Connection: inactive", "Runtime directory: missing", "Proxy: inactive", "TUN: not managed in this build", "Stale state: none"} {
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
	if got := out.String(); !strings.Contains(got, "Guidance: run `podlaz recover`") {
		t.Fatalf("expected recovery guidance in status output, got %q", got)
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
	got := out.String()
	for _, text := range []string{"podlaz doctor report", "Source: daemon", "[OK] daemon: running"} {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
}

func TestRunCLIDoctorFallsBackWhenDaemonUnavailable(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"doctor"}, &out, options{
		daemonDoctor: func(context.Context) (doctor.Report, error) {
			return doctor.Report{}, fmt.Errorf("%w: daemon socket /tmp/podlazd.sock does not exist; start podlazd", client.ErrDaemonUnavailable)
		},
		doctor: func(context.Context) doctor.Report { return cleanDoctorReport() },
	})
	if err != nil {
		t.Fatalf("doctor fallback failed: %v", err)
	}
	got := out.String()
	for _, text := range []string{"Source: local fallback", "[WARN] daemon: daemon socket /tmp/podlazd.sock does not exist; start podlazd", "[OK] platform: linux/amd64"} {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
}

func TestRunCLIDoctorRejectsUnsupportedArguments(t *testing.T) {
	for _, tt := range []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{name: "json", args: []string{"doctor", "--json"}, wantMessage: "doctor --json is not implemented yet"},
		{name: "core", args: []string{"doctor", "--core"}, wantMessage: "doctor --core is not implemented yet"},
		{name: "garbage", args: []string{"doctor", "garbage"}, wantMessage: "unsupported doctor argument"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(context.Background(), tt.args, &out)
			assertUsageError(t, err, out.String(), tt.wantMessage)
		})
	}
}

func TestRunCLILogsRunsDefaultDaemonLogs(t *testing.T) {
	var out bytes.Buffer
	var gotOptions logs.Options
	err := runWithOptions(context.Background(), []string{"logs"}, &out, options{
		logs: func(_ context.Context, w io.Writer, opts logs.Options) error {
			gotOptions = opts
			_, _ = fmt.Fprintln(w, "podlaz daemon logs")
			_, _ = fmt.Fprintln(w, "Jun 03 host podlazd[123]: daemon started")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("logs failed: %v", err)
	}
	if gotOptions != (logs.Options{}) {
		t.Fatalf("expected default logs options, got %#v", gotOptions)
	}
	if got := out.String(); !strings.Contains(got, "podlaz daemon logs") || !strings.Contains(got, "daemon started") {
		t.Fatalf("expected daemon logs output, got %q", got)
	}
}

func TestRunCLILogsParsesFollowDaemonAndSince(t *testing.T) {
	var gotOptions logs.Options
	err := runWithOptions(context.Background(), []string{"logs", "--daemon", "--since", "1 hour ago", "-f"}, &bytes.Buffer{}, options{
		logs: func(_ context.Context, _ io.Writer, opts logs.Options) error {
			gotOptions = opts
			return nil
		},
	})
	if err != nil {
		t.Fatalf("logs failed: %v", err)
	}
	if !gotOptions.Follow || gotOptions.Since != "1 hour ago" || gotOptions.Core {
		t.Fatalf("expected follow, since, and daemon log options, got %#v", gotOptions)
	}
}

func TestRunCLILogsRejectsUnsupportedArguments(t *testing.T) {
	for _, tt := range []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{name: "json", args: []string{"logs", "--json"}, wantMessage: "logs --json is not implemented yet"},
		{name: "missing-since", args: []string{"logs", "--since"}, wantMessage: "logs --since requires a value"},
		{name: "garbage", args: []string{"logs", "garbage"}, wantMessage: "unsupported logs argument"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(context.Background(), tt.args, &out)
			assertUsageError(t, err, out.String(), tt.wantMessage)
		})
	}
}

func TestRunCLIVersionRejectsArguments(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"version", "garbage"}, &out)
	assertUsageError(t, err, out.String(), "version does not accept arguments")
}

func TestRunCLIRecoverRendersDryRunPlan(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"recover"}, &out, options{
		recover: func(context.Context) recovery.PlanResult {
			return recovery.PlanResult{Candidates: []recovery.Candidate{{Kind: "tun-interface", Description: "TUN interface", Target: "podlaz0"}}}
		},
	})
	if err != nil {
		t.Fatalf("recover failed: %v", err)
	}
	got := out.String()
	for _, text := range []string{"podlaz recovery dry-run", "Would recover TUN interface: podlaz0", "No changes were applied."} {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
}

func TestRunCLIRecoverExecuteYesUsesInjectedExecutor(t *testing.T) {
	var out bytes.Buffer
	var called bool
	err := runWithOptions(context.Background(), []string{"recover", "--execute", "--yes"}, &out, options{
		recoverExecute: func(context.Context) (recovery.ExecuteResult, error) {
			called = true
			return recovery.ExecuteResult{Results: []recovery.CleanupResult{{
				Candidate: recovery.Candidate{Kind: "tun-interface", Description: "TUN interface", Target: "podlaz0"},
				Status:    "recovered",
			}}}, nil
		},
	})
	if err != nil {
		t.Fatalf("recover --execute --yes failed: %v", err)
	}
	if !called {
		t.Fatal("expected injected recover executor to be called")
	}
	if got := out.String(); !strings.Contains(got, "Mode: execute") || !strings.Contains(got, "Recovered TUN interface: podlaz0") {
		t.Fatalf("expected execute recovery output, got %q", got)
	}
}

func TestRunCLIRecoverExecuteInteractiveConfirmation(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"recover", "--execute"}, &out, options{
		stdin:           strings.NewReader("yes\n"),
		stdinIsTerminal: func() bool { return true },
		recoverExecute: func(context.Context) (recovery.ExecuteResult, error) {
			return recovery.ExecuteResult{}, nil
		},
	})
	if err != nil {
		t.Fatalf("interactive recover failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Type yes to continue") || !strings.Contains(got, "Mode: execute") {
		t.Fatalf("expected confirmation and execute output, got %q", got)
	}
}

func TestRunCLIRecoverExecuteRequiresYesInNonInteractiveMode(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"recover", "--execute"}, &out, options{
		stdinIsTerminal: func() bool { return false },
		recoverExecute: func(context.Context) (recovery.ExecuteResult, error) {
			t.Fatal("recover executor must not run without confirmation")
			return recovery.ExecuteResult{}, nil
		},
	})
	assertUsageError(t, err, out.String(), "recover --execute requires --yes in non-interactive mode")
}

func TestRunCLIRecoverExecuteJSONRequiresYes(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"recover", "--execute", "--json"}, &out, options{
		stdinIsTerminal: func() bool { return true },
		recoverExecute: func(context.Context) (recovery.ExecuteResult, error) {
			t.Fatal("recover executor must not run without --yes in JSON mode")
			return recovery.ExecuteResult{}, nil
		},
	})
	assertUsageError(t, err, out.String(), "recover --execute --json requires --yes")
}

func TestRunCLIRecoverExecuteJSONRendersResult(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"recover", "--execute", "--yes", "--json"}, &out, options{
		recoverExecute: func(context.Context) (recovery.ExecuteResult, error) {
			return recovery.ExecuteResult{Results: []recovery.CleanupResult{{
				Candidate: recovery.Candidate{Kind: "nftables-table", Description: "nftables table", Target: "inet podlaz"},
				Status:    "skipped",
				Message:   "already absent",
			}}}, nil
		},
	})
	if err != nil {
		t.Fatalf("recover --execute --yes --json failed: %v", err)
	}
	got := out.String()
	for _, text := range []string{`"mode": "execute"`, `"status": "skipped"`, `"target": "inet podlaz"`} {
		if !strings.Contains(got, text) {
			t.Fatalf("expected JSON output to contain %q, got %q", text, got)
		}
	}
}

func TestRunCLIRecoverExecuteReturnsDaemonUnavailableExitCode(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"recover", "--execute", "--yes"}, &out, options{
		recoverExecute: func(context.Context) (recovery.ExecuteResult, error) {
			return recovery.ExecuteResult{}, fmt.Errorf("%w: daemon socket /tmp/podlazd.sock does not exist; start podlazd", client.ErrDaemonUnavailable)
		},
	})
	if err == nil {
		t.Fatal("expected daemon unavailable error")
	}
	if got := ExitCode(err); got != 5 {
		t.Fatalf("expected daemon unavailable exit code 5, got %d", got)
	}
	if !strings.Contains(err.Error(), "daemon socket /tmp/podlazd.sock does not exist") {
		t.Fatalf("expected daemon unavailable detail, got %v", err)
	}
}

func TestRunCLIDoctorReturnsDiagnosticExitCodeForFailures(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"doctor"}, &out, options{
		doctor: func(context.Context) doctor.Report {
			return doctor.Report{Checks: []doctor.Check{{Name: "default-route", Severity: doctor.SeverityFail, Message: "ip route show default failed: test failure"}}}
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

func TestRunCLIRecoverExecutePropagatesRuntimeFailure(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"recover", "--execute", "--yes"}, &out, options{
		recoverExecute: func(context.Context) (recovery.ExecuteResult, error) {
			return recovery.ExecuteResult{}, errors.New("boom")
		},
	})
	if err == nil {
		t.Fatal("expected recover runtime error")
	}
	if got := ExitCode(err); got != 1 {
		t.Fatalf("expected generic runtime exit code 1, got %d", got)
	}
}
