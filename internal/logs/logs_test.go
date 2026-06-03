package logs

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBuildJournalctlArgsDefaultsToRecentDaemonLogs(t *testing.T) {
	got := BuildJournalctlArgs(Options{})
	want := []string{"--system", "--unit", DaemonUnit, "--no-pager", "--output", "short", "--lines", DefaultLines}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("journalctl args mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestBuildJournalctlArgsSupportsFollowAndSince(t *testing.T) {
	got := BuildJournalctlArgs(Options{Follow: true, Since: "1 hour ago"})
	want := []string{"--system", "--unit", DaemonUnit, "--no-pager", "--output", "short", "--since", "1 hour ago", "--follow"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("journalctl args mismatch\nwant: %#v\n got: %#v", want, got)
	}
}

func TestScanRedactedRedactsSensitiveLogContent(t *testing.T) {
	input := strings.NewReader("Jun 03 host tunwardend[123]: token=abc password=secret id=123e4567-e89b-12d3-a456-426614174000\n")
	var out bytes.Buffer

	if err := scanRedacted(&out, input); err != nil {
		t.Fatalf("scanRedacted failed: %v", err)
	}
	got := out.String()
	for _, text := range []string{"token=REDACTED", "password=REDACTED", "123e…4000"} {
		if !strings.Contains(got, text) {
			t.Fatalf("expected redacted output to contain %q, got %q", text, got)
		}
	}
	for _, text := range []string{"token=abc", "password=secret", "123e4567-e89b-12d3-a456-426614174000"} {
		if strings.Contains(got, text) {
			t.Fatalf("expected sensitive value %q to be redacted, got %q", text, got)
		}
	}
}

func TestScanRedactedHandlesLongLogLines(t *testing.T) {
	longMessage := strings.Repeat("x", 70*1024)
	input := strings.NewReader("Jun 03 host tunwardend[123]: " + longMessage + "\n")
	var out bytes.Buffer

	if err := scanRedacted(&out, input); err != nil {
		t.Fatalf("scanRedacted failed for long log line: %v", err)
	}
	if got := out.String(); !strings.Contains(got, longMessage) {
		t.Fatalf("expected long log line to be preserved, got output length %d", len(got))
	}
}

func TestScanRedactedReturnsWriteErrors(t *testing.T) {
	wantErr := errors.New("write failed")
	err := scanRedacted(errorWriter{err: wantErr}, strings.NewReader("Jun 03 host tunwardend[123]: daemon started\n"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected write error %v, got %v", wantErr, err)
	}
}

func TestRunReturnsActionableErrorWhenJournalctlIsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	var out bytes.Buffer
	err := Run(context.Background(), &out, Options{})
	if err == nil {
		t.Fatal("expected missing journalctl to fail")
	}
	if got := err.Error(); !strings.Contains(got, "journalctl is not available") || !strings.Contains(got, "systemd/journald host") {
		t.Fatalf("expected actionable journalctl error, got %q", got)
	}
	if got := out.String(); got != "" {
		t.Fatalf("expected no stdout when journalctl is missing, got %q", got)
	}
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}
