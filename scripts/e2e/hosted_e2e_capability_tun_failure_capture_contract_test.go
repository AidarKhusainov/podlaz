package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestHostedE2ECapabilityCapturesTunFailureBeforeGuestTeardown(t *testing.T) {
	script, err := os.ReadFile("hosted-e2e-capability.sh")
	if err != nil {
		t.Fatalf("read hosted capability script: %v", err)
	}
	text := string(script)

	for _, want := range []string{
		"capture_guest_tun_failure_diagnostics()",
		"local import_code connect_code",
		"connect_code=$?",
		"if (( connect_code != 0 )); then",
		"capture_guest_tun_failure_diagnostics",
		"return \"${connect_code}\"",
		"profile-tun-report-cause.log",
		"/run/podlaz/diagnostics/tun-last.json",
		"tun.report.primary_%s=observed",
		"tun.report.status_%s=observed",
		"tun.report.failure_phase_%s=observed",
		"tun.report.probe_%s_classification_%s=observed",
		"tun.report.probe_%s_failure_phase_%s=observed",
		"^[A-Za-z0-9_.-]+$",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("hosted capability script must synchronously capture bounded structured TUN diagnostics before teardown; missing %q", want)
		}
	}

	connect := strings.Index(text, "/usr/bin/podlaz connect --mode tun")
	capture := strings.Index(text[connect:], "capture_guest_tun_failure_diagnostics")
	requested := strings.Index(text[connect:], "record_capability tun.connect_requested pass")
	if connect < 0 || capture < 0 || requested < 0 {
		t.Fatalf("hosted capability TUN connect sequence is incomplete")
	}
	if capture >= requested {
		t.Fatalf("late TUN failure diagnostics must be captured before the successful connect marker")
	}
}
