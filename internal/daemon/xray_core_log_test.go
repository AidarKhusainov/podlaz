package daemon

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestCoreLogWriterFlushesFinalLineWithoutTrailingNewline(t *testing.T) {
	var out bytes.Buffer
	restoreLog := captureLogOutput(&out)
	defer restoreLog()

	writer := newCoreLogWriter("test-profile", "stderr")
	writer.setPID(42)
	if _, err := writer.Write([]byte("final crash line without newline")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	writer.Flush()

	got := out.String()
	for _, text := range []string{"podlazd: core xray stderr", "pid=42", "profile=test-profile", "final crash line without newline"} {
		if !strings.Contains(got, text) {
			t.Fatalf("expected core log output to contain %q, got %q", text, got)
		}
	}
}

func TestCoreLogWriterSplitsCompleteLinesAndFlushesTail(t *testing.T) {
	var out bytes.Buffer
	restoreLog := captureLogOutput(&out)
	defer restoreLog()

	writer := newCoreLogWriter("test-profile", "stdout")
	writer.setPID(43)
	if _, err := writer.Write([]byte("first line\nsecond line without newline")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	writer.Flush()

	got := out.String()
	for _, text := range []string{"first line", "second line without newline"} {
		if !strings.Contains(got, text) {
			t.Fatalf("expected core log output to contain %q, got %q", text, got)
		}
	}
	if count := strings.Count(got, "podlazd: core xray stdout"); count != 2 {
		t.Fatalf("expected two core stdout log lines, got %d: %q", count, got)
	}
}

func TestCoreLogWriterDoesNotEmitPIDZeroForOutputBeforePIDIsKnown(t *testing.T) {
	var out bytes.Buffer
	restoreLog := captureLogOutput(&out)
	defer restoreLog()

	writer := newCoreLogWriter("test-profile", "stderr")
	if _, err := writer.Write([]byte("early line\n")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if got := out.String(); got != "" {
		t.Fatalf("expected no log output before pid is known, got %q", got)
	}

	writer.setPID(44)
	writer.Flush()
	got := out.String()
	for _, text := range []string{"podlazd: core xray stderr", "pid=44", "profile=test-profile", "early line"} {
		if !strings.Contains(got, text) {
			t.Fatalf("expected core log output to contain %q, got %q", text, got)
		}
	}
	if strings.Contains(got, "pid=0") {
		t.Fatalf("expected no pid=0 in core log output, got %q", got)
	}
}

func captureLogOutput(out *bytes.Buffer) func() {
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(out)
	log.SetFlags(0)
	return func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	}
}
