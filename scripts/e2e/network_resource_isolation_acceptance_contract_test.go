package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestNetworkResourceIsolationPackageAcceptanceCoversPrivacyAndTerminalBoundaries(t *testing.T) {
	data, err := os.ReadFile("network-resource-isolation-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read network-resource-isolation acceptance: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"privacy_envelope_armed",
		"sudo -n systemctl kill --kill-who=main -s KILL podlazd.service",
		"assert_direct_uplink_blocked",
		"daemon_crash_recovered_without_manual_repair",
		"PODLAZ_E2E_TUN_TERMINAL_FAILURE",
		"terminal-failure.trigger",
		"terminal_failure_triggered",
		"terminal-data-plane-clean.ready",
		"assert_privacy_envelope_present",
		"terminal_disconnected",
		"terminal_no_auto_reconnect",
		"ordinary_network_after_terminal",
		"foreign_collision_survived",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("network-resource-isolation acceptance must contain %q", required)
		}
	}
	if strings.Contains(script, "network-resource-isolation-disconnect") {
		t.Fatal("terminal scenario must be driven by a terminal revalidation failure, not explicit disconnect")
	}
}

func TestNetworkResourceIsolationPackageAcceptanceDoesNotRepairProductStateManually(t *testing.T) {
	data, err := os.ReadFile("network-resource-isolation-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read network-resource-isolation acceptance: %v", err)
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
			t.Fatalf("network-resource-isolation success path must not contain manual product-state repair %q", forbidden)
		}
	}
}

func TestNetworkResourceIsolationWorkflowRunsInstalledPackageAcceptance(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/e2e-tun-package-convergence.yml")
	if err != nil {
		t.Fatalf("read package convergence workflow: %v", err)
	}
	workflow := string(data)
	for _, required := range []string{
		"Run network resource isolation acceptance",
		"bash scripts/e2e/network-resource-isolation-package-acceptance.sh",
		"timeout-minutes:",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("network-resource-isolation workflow wiring must contain %q", required)
		}
	}
}
