package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestNetworkReconciliationPackageAcceptanceCoversEvidenceDrivenReconciliation(t *testing.T) {
	data, err := os.ReadFile("network-reconciliation-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read network-reconciliation acceptance: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"soft_provider_failure_stayed_connected",
		"resolved_convergence_recovered",
		"route_replacement_recovered",
		"surrounding_tun_preserved",
		"suspend_resume_recovered",
		"dhcp_churn_recovered",
		"reconciliation-rebuild.ready",
		"privacy_envelope_present_during_rebuild",
		"core_exit_degraded_source_rebuilt",
		"terminal_failure_bounded",
		"terminal_no_auto_reconnect",
		"ordinary_network_after_terminal",
		"rtcwake -m mem",
		"nmcli connection down",
		"nmcli connection up",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("network-reconciliation acceptance must contain %q", required)
		}
	}
}

func TestNetworkReconciliationPackageAcceptanceDoesNotRepairPodlazStateManually(t *testing.T) {
	data, err := os.ReadFile("network-reconciliation-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read network-reconciliation acceptance: %v", err)
	}
	lower := strings.ToLower(string(data))
	for _, forbidden := range []string{
		"podlaz recover --execute",
		"ip rule del",
		"ip route del default",
		"resolvectl revert podlaz0",
		"nft delete table inet podlaz_pe_",
		"systemctl restart networkmanager",
		"systemctl restart systemd-resolved",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("network-reconciliation success path must not contain manual Podlaz repair %q", forbidden)
		}
	}
}

func TestNetworkReconciliationWorkflowRunsInstalledPackageAcceptance(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/e2e-tun-package-convergence.yml")
	if err != nil {
		t.Fatalf("read package convergence workflow: %v", err)
	}
	workflow := string(data)
	for _, required := range []string{
		"Run evidence-driven reconciliation acceptance",
		"PODLAZ_E2E_ALLOW_HOST_CHURN: 'true'",
		"bash scripts/e2e/network-reconciliation-package-acceptance.sh",
		"timeout-minutes: 60",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("network-reconciliation workflow wiring must contain %q", required)
		}
	}
}
