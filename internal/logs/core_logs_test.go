package logs

import (
	"bytes"
	"strings"
	"testing"
)

func TestScanRedactedFilteredShowsOnlyCoreLines(t *testing.T) {
	input := strings.NewReader(strings.Join([]string{
		"Jun 03 host podlazd[123]: podlazd: daemon started",
		"Jun 03 host podlazd[123]: podlazd: core xray started pid=42 profile=test",
		"Jun 03 host podlazd[123]: podlazd: core xray stderr pid=42 profile=test: token=secret",
		"Jun 03 host podlazd[123]: podlazd: status request handled",
	}, "\n") + "\n")
	var out bytes.Buffer

	count, err := scanRedactedFiltered(&out, input, isCoreLogLine)
	if err != nil {
		t.Fatalf("scanRedactedFiltered failed: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 core log lines, got %d: %q", count, out.String())
	}
	got := out.String()
	for _, text := range []string{"core xray started", "core xray stderr", "token=REDACTED"} {
		if !strings.Contains(got, text) {
			t.Fatalf("expected filtered output to contain %q, got %q", text, got)
		}
	}
	for _, text := range []string{"daemon started", "status request handled", "token=secret"} {
		if strings.Contains(got, text) {
			t.Fatalf("expected filtered output to omit %q, got %q", text, got)
		}
	}
}

func TestIsCoreLogLineAcceptsSystemdChildProcessPrefix(t *testing.T) {
	if !isCoreLogLine("Jun 03 host xray[5678]: started") {
		t.Fatal("expected xray process journal line to be treated as a core log")
	}
	if isCoreLogLine("Jun 03 host podlazd[123]: podlazd: status request handled") {
		t.Fatal("expected daemon status line not to be treated as a core log")
	}
}
