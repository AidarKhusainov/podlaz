package daemon

import (
	"bytes"
	"errors"
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
	writer := newCoreLogWriter(profileSentinel, "stderr")
	writer.setPID(42)
	payload := strings.Join([]string{
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

func TestCoreLifecycleLogsNeverPersistProfileOrRawErrorText(t *testing.T) {
	var out bytes.Buffer
	restoreLog := captureLogOutput(&out)
	defer restoreLog()

	const profileSentinel = "profile-private-fixture.example.invalid"
	const errorSentinel = "dial 203.0.113.88 token=fixture-secret-value"

	logCoreStarted(43, profileSentinel)
	logCoreStartFailed(profileSentinel, errors.New(errorSentinel))
	logCoreStopped(43, profileSentinel)
	logCoreExited(43, profileSentinel, errorSentinel)

	got := out.String()
	for _, forbidden := range []string{profileSentinel, "203.0.113.88", "fixture-secret-value", errorSentinel} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("daemon lifecycle log leaked %q in %q", forbidden, got)
		}
	}
	for _, want := range []string{"core xray started", "core xray start failed", "core xray stopped", "core xray exited"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected safe structural lifecycle event %q in %q", want, got)
		}
	}
}
