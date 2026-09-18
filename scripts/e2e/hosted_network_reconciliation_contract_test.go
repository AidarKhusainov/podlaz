package e2e

import (
	"os"
	"strings"
	"testing"
)

const (
	hostedNetworkReconciliationWorkflow = "../../.github/workflows/hosted-recovery.yml"
	hostedNetworkReconciliationScenario = "hosted-network-reconciliation.sh"
)

func TestHostedNetworkReconciliationWorkflow(t *testing.T) {
	workflow := readHostedNetworkReconciliationFile(t, hostedNetworkReconciliationWorkflow)
	requireHostedNetworkReconciliationMarkers(t, workflow,
		"network-reconciliation:",
		"name: Network reconciliation (${{ matrix.name }})",
		"fail-fast: false",
		"scenario: provider-observation",
		"name: Provider observation failure",
		"scenario: resolved-unknown",
		"name: Resolved unknown convergence",
		"scenario: route-replacement",
		"name: Surrounding route replacement",
		"scenario: networkmanager-uplink",
		"name: NetworkManager uplink down/up",
		"go test ./scripts/e2e -run '^TestHostedNetworkReconciliation' -count=1",
		"bash scripts/e2e/hosted-network-reconciliation.sh \"${{ matrix.scenario }}\" \"${DEV_DEB}\"",
		"hosted-network-reconciliation-${{ matrix.scenario }}.txt",
		"podlaz-hosted-network-reconciliation-${{ matrix.scenario }}",
	)
}

func TestHostedNetworkReconciliationScenario(t *testing.T) {
	script := readHostedNetworkReconciliationFile(t, hostedNetworkReconciliationScenario)
	requireHostedNetworkReconciliationMarkers(t, script,
		`BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		"PODLAZ_E2E_TUN_RECONCILIATION_SOFT_FAILURE=true",
		"PODLAZ_E2E_TUN_RECONCILIATION_RESOLVED_UNKNOWN=true",
		"reconciliation-soft-provider.trigger",
		"reconciliation-soft-provider.injected",
		"reconciliation-resolved-unknown.trigger",
		"reconciliation-resolved-unknown.injected",
		"ip -4 route replace blackhole",
		"nmcli connection down \"${UPLINK_CONNECTION}\"",
		"nmcli connection up \"${UPLINK_CONNECTION}\"",
		"assert_privacy_envelope_present",
		"assert_direct_uplink_blocked",
		"assert_foreign_fixture",
		"wait_for_verified_active",
		"assert_revalidated_active_authority",
		"reconciliation.fault_injected",
		"privacy.envelope_retained",
		"privacy.direct_uplink_blocked",
		"foreign.state_preserved",
		"reconciliation.verified_active",
		"base.terminal_cleanup",
		"artifact.privacy",
	)
}

func TestHostedNetworkReconciliationDoesNotRepairPodlazAuthority(t *testing.T) {
	script := strings.ToLower(readHostedNetworkReconciliationFile(t, hostedNetworkReconciliationScenario))
	for _, forbidden := range []string{
		"podlaz recover --execute",
		"ip rule del",
		"ip route del default",
		"resolvectl revert podlaz0",
		"nft delete table inet podlaz_pe_",
		"systemctl restart networkmanager",
		"systemctl restart systemd-resolved",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("hosted network reconciliation must not repair Podlaz authority with %q", forbidden)
		}
	}
}

func readHostedNetworkReconciliationFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireHostedNetworkReconciliationMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			t.Fatalf("missing %q", marker)
		}
	}
}
