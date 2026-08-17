package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestIssue254RemoteClientAcceptanceUsesNormalOrdinaryUserWithoutPodlazGroup(t *testing.T) {
	data, err := os.ReadFile("issue254-remote-client-acceptance.sh")
	if err != nil {
		t.Fatalf("read issue 254 remote-client acceptance: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"id -nG",
		"ordinary-user acceptance must not run as root",
		"ordinary_user_without_podlaz_group",
		"connect --mode proxy-only",
		"recover --json",
		"Startup recovery scan: clean for active connection",
		"logs \"--${mode}\" --since 36h",
		"proxy_status_doctor_recover_consistent",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("issue 254 acceptance lost %q", required)
		}
	}
	for _, forbidden := range []string{
		"runuser",
		"usermod",
		"gpasswd",
		`-g podlaz`,
		`-G podlaz`,
		`-G "${LOG_READER_ACCESS_GROUP}"`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("issue 254 ordinary-user acceptance must preserve the login identity without granting or rewriting groups: %q", forbidden)
		}
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
