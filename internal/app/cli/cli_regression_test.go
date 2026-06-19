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
)

func TestRunCLIHelpStatus(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"help", "status"}, &out); err != nil {
		t.Fatalf("help status failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Report local podlaz runtime state") {
		t.Fatalf("expected status help output, got %q", got)
	}
}

func TestRunCLIDoctorHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"doctor", "--help"}, &out); err != nil {
		t.Fatalf("doctor --help failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Usage:\n  podlaz doctor") {
		t.Fatalf("expected doctor help output, got %q", got)
	}
}

func TestRunCLIHelpDoctor(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"help", "doctor"}, &out); err != nil {
		t.Fatalf("help doctor failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "daemon-backed diagnostics") {
		t.Fatalf("expected doctor help output, got %q", got)
	}
}

func TestRunCLIDoctorFallsBackOnDaemonTimeout(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"doctor"}, &out, options{
		daemonDoctor: func(context.Context) (doctor.Report, error) {
			return doctor.Report{}, fmt.Errorf("%w: daemon socket /tmp/podlazd.sock did not respond before timeout; start or restart podlazd", client.ErrDaemonUnavailable)
		},
		doctor: func(context.Context) doctor.Report { return cleanDoctorReport() },
	})
	if err != nil {
		t.Fatalf("doctor timeout fallback failed: %v", err)
	}
	got := out.String()
	for _, text := range []string{"Source: local fallback", "[WARN] daemon: daemon socket /tmp/podlazd.sock did not respond before timeout; start or restart podlazd"} {
		if !strings.Contains(got, text) {
			t.Fatalf("expected output to contain %q, got %q", text, got)
		}
	}
}

func TestRunCLILogsHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"logs", "--help"}, &out); err != nil {
		t.Fatalf("logs --help failed: %v", err)
	}
	got := out.String()
	for _, text := range []string{"Usage:\n  podlaz logs", "journalctl", "--core", "podlaz logs"} {
		if !strings.Contains(got, text) {
			t.Fatalf("expected logs help output to contain %q, got %q", text, got)
		}
	}
}

func TestRunCLIHelpLogs(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"help", "logs"}, &out); err != nil {
		t.Fatalf("help logs failed: %v", err)
	}
	got := out.String()
	for _, text := range []string{"podlaz logs", "--core", "journalctl"} {
		if !strings.Contains(got, text) {
			t.Fatalf("expected logs help output to contain %q, got %q", text, got)
		}
	}
}

func TestRunCLILogsParsesCore(t *testing.T) {
	var gotOptions logs.Options
	err := runWithOptions(context.Background(), []string{"logs", "--core"}, &bytes.Buffer{}, options{
		logs: func(_ context.Context, _ io.Writer, opts logs.Options) error {
			gotOptions = opts
			return nil
		},
	})
	if err != nil {
		t.Fatalf("logs --core failed: %v", err)
	}
	if !gotOptions.Core {
		t.Fatalf("expected core logs option, got %#v", gotOptions)
	}
}

func TestRunCLILogsAcceptsJournalctlCompatibleSinceValues(t *testing.T) {
	for _, tt := range []struct {
		name      string
		args      []string
		wantSince string
	}{
		{name: "negative-relative-token", args: []string{"logs", "--since", "-1h"}, wantSince: "-1h"},
		{name: "negative-relative-equals", args: []string{"logs", "--since=-30min"}, wantSince: "-30min"},
		{name: "positive-relative-token", args: []string{"logs", "--since", "+5min"}, wantSince: "+5min"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var gotOptions logs.Options
			err := runWithOptions(context.Background(), tt.args, &bytes.Buffer{}, options{
				logs: func(_ context.Context, _ io.Writer, opts logs.Options) error {
					gotOptions = opts
					return nil
				},
			})
			if err != nil {
				t.Fatalf("logs failed: %v", err)
			}
			if gotOptions.Since != tt.wantSince {
				t.Fatalf("expected since %q, got %#v", tt.wantSince, gotOptions)
			}
		})
	}
}

func TestRunCLILogsRejectsSinceWithoutValueRegression(t *testing.T) {
	for _, tt := range []struct {
		name        string
		args        []string
		wantMessage string
	}{
		{name: "option-since", args: []string{"logs", "--since", "--follow"}, wantMessage: "logs --since requires a value"},
		{name: "empty-since-equals", args: []string{"logs", "--since="}, wantMessage: "logs --since requires a value"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(context.Background(), tt.args, &out)
			assertUsageError(t, err, out.String(), tt.wantMessage)
		})
	}
}
