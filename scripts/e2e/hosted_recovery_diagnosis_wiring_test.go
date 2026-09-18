package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestHostedDaemonRecoveryWiresBoundedResumeDiagnosis(t *testing.T) {
	data, err := os.ReadFile("hosted-daemon-recovery.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, marker := range []string{
		`RESUME_DIAGNOSTIC="/run/podlaz/diagnostics/network-session-resume.json"`,
		`diagnose-active`,
		`"${RECOVERY_PRIVATE}/status.json"`,
		`"${RESUME_DIAGNOSTIC}"`,
		`daemon.same_boot_resume.${diagnosis}`,
	} {
		if !strings.Contains(script, marker) {
			t.Fatalf("hosted daemon recovery diagnosis wiring lost %q", marker)
		}
	}
}
