package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestIssue259CandidateUpgradeDoesNotManuallyStartService(t *testing.T) {
	data, err := os.ReadFile("issue259-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read issue259 acceptance: %v", err)
	}
	script := string(data)
	candidate := shellFunctionBody(t, script, "install_candidate_package")
	if !strings.Contains(candidate, "apt install") {
		t.Fatal("candidate install helper must install the candidate package")
	}
	if strings.Contains(candidate, "systemctl start") {
		t.Fatal("candidate package acceptance must not manually start podlazd after apt install")
	}
	for _, required := range []string{"systemctl is-active", "main_pid", "previous_pid"} {
		if !strings.Contains(candidate, required) {
			t.Fatalf("candidate package acceptance must verify %q after package replacement", required)
		}
	}
}

func TestIssue259BaselineSetupMayStartServiceExplicitly(t *testing.T) {
	data, err := os.ReadFile("issue259-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read issue259 acceptance: %v", err)
	}
	baseline := shellFunctionBody(t, string(data), "install_setup_package")
	if !strings.Contains(baseline, "systemctl start") {
		t.Fatal("baseline setup helper may explicitly start podlazd to establish the test fixture")
	}
}

func shellFunctionBody(t *testing.T, script, name string) string {
	t.Helper()
	startMarker := name + "() {"
	start := strings.Index(script, startMarker)
	if start < 0 {
		t.Fatalf("missing shell function %s", name)
	}
	bodyStart := start + len(startMarker)
	end := strings.Index(script[bodyStart:], "\n}")
	if end < 0 {
		t.Fatalf("unterminated shell function %s", name)
	}
	return script[bodyStart : bodyStart+end]
}
