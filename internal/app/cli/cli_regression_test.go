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
	if got := out.String(); !strings.Contains(got, "product connection state") {
		t.Fatalf("status help: %q", got)
	}
}

func TestRunCLIDebugHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"debug", "--help"}, &out); err != nil {
		t.Fatalf("debug --help failed: %v", err)
	}
	got := out.String()
	for _, want := range []string{"debug doctor", "debug logs", "debug proxy", "debug recover"} {
		if !strings.Contains(got, want) {
			t.Fatalf("debug help missing %q: %q", want, got)
		}
	}
}

func TestRunCLIDebugDoctorHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"debug", "doctor", "--help"}, &out); err != nil {
		t.Fatalf("debug doctor --help failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Usage:\n  podlaz debug doctor") {
		t.Fatalf("doctor help: %q", got)
	}
}

func TestRunCLIDebugDoctorFallsBackOnDaemonTimeout(t *testing.T) {
	var out bytes.Buffer
	err := runWithOptions(context.Background(), []string{"debug", "doctor"}, &out, options{
		daemonDoctor: func(context.Context) (doctor.Report, error) {
			return doctor.Report{}, fmt.Errorf("%w: daemon socket /tmp/podlazd.sock did not respond before timeout; start or restart podlazd", client.ErrDaemonUnavailable)
		},
		doctor: func(context.Context) doctor.Report { return cleanDoctorReport() },
	})
	if err != nil {
		t.Fatalf("doctor timeout fallback failed: %v", err)
	}
	got := out.String()
	for _, text := range []string{"Source: local fallback", "[WARN] daemon:"} {
		if !strings.Contains(got, text) {
			t.Fatalf("doctor output missing %q: %q", text, got)
		}
	}
}

func TestRunCLIDebugLogsHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"debug", "logs", "--help"}, &out); err != nil {
		t.Fatalf("debug logs --help failed: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "Usage:\n  podlaz debug logs") {
		t.Fatalf("logs help: %q", got)
	}
}

func TestRunCLIDebugLogsParsesCore(t *testing.T) {
	var gotOptions logs.Options
	err := runWithOptions(context.Background(), []string{"debug", "logs", "--core"}, &bytes.Buffer{}, options{
		logs: func(_ context.Context, _ io.Writer, opts logs.Options) error {
			gotOptions = opts
			return nil
		},
	})
	if err != nil {
		t.Fatalf("debug logs --core failed: %v", err)
	}
	if !gotOptions.Core {
		t.Fatalf("expected core logs option, got %#v", gotOptions)
	}
}

func TestRunCLIDebugLogsRejectsJournalctlNativeSinceValues(t *testing.T) {
	for _, args := range [][]string{
		{"debug", "logs", "--since", "-1h"},
		{"debug", "logs", "--since=-30m"},
		{"debug", "logs", "--since", "+5m"},
		{"debug", "logs", "--since", "yesterday"},
	} {
		var out bytes.Buffer
		err := runWithOptions(context.Background(), args, &out, options{
			logs: func(context.Context, io.Writer, logs.Options) error {
				t.Fatal("invalid --since reached backend")
				return nil
			},
		})
		assertUsageError(t, err, out.String(), "invalid logs --since duration")
	}
}
