package daemon

import (
	"bytes"
	"strings"
	"testing"
)

func TestCoreLogWriterNeverPersistsUntrustedChildOutput(t *testing.T) {
	var out bytes.Buffer
	restoreLog := captureLogOutput(&out)
	defer restoreLog()

	const (
		profileSentinel = "vless-fixture-profile-secret"
		domainSentinel  = "private-fixture.example.invalid"
		ipSentinel      = "203.0.113.77"
		idSentinel      = "123e4567-e89b-12d3-a456-426614174000"
		pathSentinel    = "/private/fixture/runtime.json"
	)
	writer := newCoreLogWriter("stderr")
	writer.setPID(42)
	payload := strings.Join([]string{
		"profile=" + profileSentinel,
		"accepted tcp:" + ipSentinel + ":443",
		"host=" + domainSentinel + " id=" + idSentinel,
		"config=" + pathSentinel,
		"unrecognized payload token=fixture-secret-value",
	}, "\n")
	if _, err := writer.Write([]byte(payload)); err != nil {
		t.Fatalf("write child output: %v", err)
	}
	writer.Flush()

	got := out.String()
	for _, forbidden := range []string{profileSentinel, domainSentinel, ipSentinel, idSentinel, pathSentinel, "fixture-secret-value", "accepted tcp:"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("daemon journal leaked untrusted child data %q in %q", forbidden, got)
		}
	}
	for _, want := range []string{"podlazd: core xray stderr", "pid=42"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected safe structural core event %q in %q", want, got)
		}
	}
}

func TestCoreLogWriterSanitizesUnexpectedStreamName(t *testing.T) {
	var out bytes.Buffer
	restoreLog := captureLogOutput(&out)
	defer restoreLog()

	const streamSentinel = "private-fixture.example.invalid"
	writer := newCoreLogWriter(streamSentinel)
	writer.setPID(41)
	if _, err := writer.Write([]byte("opaque child output")); err != nil {
		t.Fatalf("write child output: %v", err)
	}

	got := out.String()
	if strings.Contains(got, streamSentinel) || strings.Contains(got, "opaque child output") {
		t.Fatalf("unexpected stream input or child payload crossed journal privacy boundary: %q", got)
	}
	if !strings.Contains(got, "core xray unknown output received pid=41") {
		t.Fatalf("expected low-cardinality fallback stream label, got %q", got)
	}
}

func TestCoreLifecycleLogsExposeOnlyStructuralFacts(t *testing.T) {
	var out bytes.Buffer
	restoreLog := captureLogOutput(&out)
	defer restoreLog()

	logCoreStarted(43)
	logCoreStartFailed()
	logCoreStopped(43)
	logCoreExited(43)

	got := out.String()
	for _, want := range []string{"core xray started pid=43", "core xray start failed", "core xray stopped pid=43", "core xray exited pid=43"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected safe structural lifecycle event %q in %q", want, got)
		}
	}
}
