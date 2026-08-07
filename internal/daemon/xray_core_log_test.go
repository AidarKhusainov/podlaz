package daemon

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestCoreLogWriterSuppressesFinalPayloadWithoutTrailingNewline(t *testing.T) {
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
	for _, want := range []string{"podlazd: core xray stderr output received", "pid=42"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected structural core log output to contain %q, got %q", want, got)
		}
	}
	for _, forbidden := range []string{"test-profile", "final crash line without newline"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("core journal must not contain child payload or profile metadata %q: %q", forbidden, got)
		}
	}
}

func TestCoreLogWriterCoalescesArbitraryPayloadIntoOneStructuralEvent(t *testing.T) {
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
	if count := strings.Count(got, "podlazd: core xray stdout output received"); count != 1 {
		t.Fatalf("expected one structural core stdout event, got %d: %q", count, got)
	}
	for _, forbidden := range []string{"test-profile", "first line", "second line without newline"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("core journal must not contain child payload or profile metadata %q: %q", forbidden, got)
		}
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
	for _, want := range []string{"podlazd: core xray stderr output received", "pid=44"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected structural core log output to contain %q, got %q", want, got)
		}
	}
	for _, forbidden := range []string{"pid=0", "test-profile", "early line"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("unexpected unsafe or invalid core journal value %q in %q", forbidden, got)
		}
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
