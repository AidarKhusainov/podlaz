package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestIssue261PackageAcceptanceCoversPrivacyAndTerminalBoundaries(t *testing.T) {
	data, err := os.ReadFile("issue261-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read issue261 acceptance: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"privacy_envelope_armed",
		"systemctl kill --kill-who=main -s KILL podlazd.service",
		"assert_direct_uplink_blocked",
		"daemon_crash_recovered_without_manual_repair",
		"terminal-data-plane-clean.ready",
		"assert_privacy_envelope_present",
		"terminal_disconnected",
		"terminal_no_auto_reconnect",
		"ordinary_network_after_terminal",
		"foreign_collision_survived",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("issue 261 acceptance must contain %q", required)
		}
	}
}

func TestIssue261PackageAcceptanceDoesNotRepairProductStateManually(t *testing.T) {
	data, err := os.ReadFile("issue261-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read issue261 acceptance: %v", err)
	}
	lower := strings.ToLower(string(data))
	for _, forbidden := range []string{
		"podlaz recover --execute",
		"ip rule del",
		"ip route del",
		"resolvectl revert podlaz0",
		"nft delete table inet podlaz_pe_",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("issue 261 success path must not contain manual product-state repair %q", forbidden)
		}
	}
}

func TestIssue261WorkflowRunsInstalledPackageAcceptance(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/e2e-tun-package-convergence.yml")
	if err != nil {
		t.Fatalf("read package convergence workflow: %v", err)
	}
	workflow := string(data)
	for _, required := range []string{
		"Run issue 261 privacy-envelope acceptance",
		"bash scripts/e2e/issue261-package-acceptance.sh",
		"timeout-minutes:",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("issue 261 workflow wiring must contain %q", required)
		}
	}
}
