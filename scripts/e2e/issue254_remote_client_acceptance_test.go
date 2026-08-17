package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestIssue254RemoteClientAcceptancePreservesLeastPrivilegeAndLogSemantics(t *testing.T) {
	data, err := os.ReadFile("issue254-remote-client-acceptance.sh")
	if err != nil {
		t.Fatalf("read issue 254 remote-client acceptance: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"-g \"${LOG_READER_PRIMARY_GROUP}\"",
		"-G \"${LOG_READER_ACCESS_GROUP}\"",
		"systemd-journal",
		"logs --daemon",
		"logs --daemon --since 30s",
		"logs --daemon --follow",
		"outside_group_denied",
		"bounded_tail_as_ordinary_user",
		"bounded_since_as_ordinary_user",
		"follow_streams_new_entry",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("issue 254 acceptance lost %q", required)
		}
	}
	if strings.Contains(script, "sudo -n /usr/bin/podlaz") || strings.Contains(script, "sudo /usr/bin/podlaz") {
		t.Fatal("issue 254 acceptance must not validate logs by running the CLI itself as root")
	}
}

func TestTunPackageConvergenceRunsIssue254RemoteClientAcceptance(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/e2e-tun-package-convergence.yml")
	if err != nil {
		t.Fatalf("read TUN package convergence workflow: %v", err)
	}
	if !strings.Contains(string(data), "bash scripts/e2e/issue254-remote-client-acceptance.sh") {
		t.Fatal("TUN package convergence must run issue 254 remote-client acceptance")
	}
}
