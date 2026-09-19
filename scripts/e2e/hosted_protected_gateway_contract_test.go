package e2e_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const (
	hostedProtectedGatewayWorkflow = "../../.github/workflows/hosted-recovery.yml"
	hostedProtectedGatewayScenario = "hosted-protected-gateway-lifecycle.sh"
	legacyProtectedGatewayScenario = "protected-gateway-package-acceptance.sh"
)

func readProtectedGatewayFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireProtectedGatewayMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			t.Fatalf("protected gateway contract lost %q", marker)
		}
	}
}

func forbidProtectedGatewayMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			t.Fatalf("protected gateway contract contains forbidden %q", marker)
		}
	}
}

func TestHostedProtectedGatewayLifecycleCoversLegacyAndHostedGaps(t *testing.T) {
	legacy := readProtectedGatewayFile(t, legacyProtectedGatewayScenario)
	requireProtectedGatewayMarkers(t, legacy,
		"assert_active_status",
		"wait_for_exact_exit_zero_missing_status",
		"assert_inactive_status",
		"assert_recover_json_clean",
		"assert_recover_execute_clean",
		"run_cycle first",
		"run_cycle immediate-reconnect",
	)

	script := readProtectedGatewayFile(t, hostedProtectedGatewayScenario)
	requireProtectedGatewayMarkers(t, script,
		"recover.active_inspection_noop",
		"recover.active_execute_noop",
		"authority.protected_gateway_current",
		"authority.resolver_current",
		"active.reads_stable",
		"resolver.missing_link_converged",
		"authority.observation_never_authority",
		"first.terminal_cleanup",
		"reconnect.fresh_generation",
		"second.terminal_cleanup",
		"privacy.envelope_preserved",
		"foreign.state_preserved",
		"base.terminal_cleanup",
		"artifact.privacy",
		"assert_active_authority",
		"assert_recover_dry_run_noop",
		"assert_recover_execute_noop",
		"wait_resolved_missing_link",
		"assert_fresh_generation",
	)
	forbidProtectedGatewayMarkers(t, script,
		"ip link del podlaz0",
		"ip route flush",
		"ip rule flush",
		"nft flush",
		"rm -rf /run/podlaz",
		"table 51820",
		"priority 10000",
		"10.250.0.",
	)
	cmd := exec.Command("bash", "-n", hostedProtectedGatewayScenario)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedProtectedGatewayScenario, err, output)
	}
}

func TestHostedProtectedGatewayLifecycleIsPermanentRecoveryJob(t *testing.T) {
	workflow := readProtectedGatewayFile(t, hostedProtectedGatewayWorkflow)
	requireProtectedGatewayMarkers(t, workflow,
		"protected-gateway-lifecycle:",
		"name: Protected gateway recovery lifecycle",
		"runs-on: ubuntu-24.04",
		"bash scripts/e2e/hosted-protected-gateway-lifecycle.sh",
		"podlaz-hosted-protected-gateway-lifecycle",
		"hosted-protected-gateway-lifecycle.txt",
	)
}

func TestHostedProtectedGatewayLifecycleReusesSyntheticSubstrate(t *testing.T) {
	script := readProtectedGatewayFile(t, hostedProtectedGatewayScenario)
	requireProtectedGatewayMarkers(t, script,
		`BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		`ACTIVE_AUTHORITY_HELPER="/workspace/scripts/e2e/hosted_synthetic_active_authority.py"`,
		`NETWORK_AUTHORITY_HELPER="/workspace/scripts/e2e/hosted_synthetic_network_authority.py"`,
		`PODLAZ_E2E_HOSTED_CONTROL_PHASES="candidate-ready"`,
	)
	forbidProtectedGatewayMarkers(t, script,
		"debootstrap ",
		"systemd-nspawn ",
		"ip link add dev pzsynt",
		"start_synthetic_xray_endpoint",
		"test-side repair",
	)
}
