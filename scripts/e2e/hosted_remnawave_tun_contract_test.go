package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestHostedRemnawaveTUNPreservesQ29IsolationAndLifecycle(t *testing.T) {
	data, err := os.ReadFile("hosted-remnawave-tun.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		`source "${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		"remnawave-fixture.sh",
		"export-profile",
		"machinectl copy-to",
		"chmod 0600",
		"remove_guest_private_state",
		"classify_provider_tun_connect_failure",
		"network_apply_failure|network_verify_failure|ownership_invalid|owned_state_invalid",
		"server_bypass*|dns_*|tcp_*|tls_*|https_*|doh_*|ipv6_*|likely_pmtu_blackhole|timeout",
		"assert_guest_package_provenance",
		"assert_ordinary_user_boundary",
		"wait_guest_status verified-active",
		"assert_verified_active_authority",
		"getent ahostsv4 example.com",
		"/dev/tcp/${PROBE_IP}/443",
		"openssl s_client",
		"curl -4 -fsS -o /dev/null https://example.com/",
		"remnawave_node_access_count",
		"tun.remnawave_path",
		"--interface \"${GUEST_IF}\"",
		"run_tun_doctor",
		"assert_foreign_sentinel",
		"assert_terminal_authority_clean",
		"run_clean_recovery",
		"assert_guest_network_baseline_restored",
		"assert_ordinary_connectivity_restored",
		"fixture.cleanup",
		"finalize_report\n  assert_public_artifact_privacy",
		`re.compile(r"failure\.class=`,
		"product|remnawave|fixture|infrastructure|capability|diagnostic_unknown|none",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("ephemeral Remnawave TUN scenario lost contract %q", required)
		}
	}
	for _, forbidden := range []string{
		"self-hosted",
		"PODLAZ_E2E_PROFILE_URI",
		"PODLAZ_E2E_PROFILE_URI_LIST",
		"PODLAZ_E2E_EXPECTED_EGRESS_IP",
		`[[ "${ACTIVE_EGRESS}" != "${ORDINARY_EGRESS}" ]]`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("ephemeral Remnawave TUN scenario contains retired provider assumption %q", forbidden)
		}
	}
}

func TestIntegrationKeepsRemnawaveSignalsSeparateAndSecretFree(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/integration.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"- cron: '17 3 * * *'",
		"- cron: '47 4 * * 0'",
		"remnawave_subscription:",
		"remnawave_proxy:",
		"remnawave_tun:",
		"remnawave-subscription:",
		"remnawave-proxy:",
		"remnawave-tun:",
		"needs: remnawave-proxy",
		"github.ref == 'refs/heads/master'",
		"inputs.remnawave_subscription",
		"inputs.remnawave_proxy",
		"inputs.remnawave_tun",
		"remnawave-subscription-acceptance.sh",
		"remnawave-proxy-acceptance.sh",
		"hosted-remnawave-tun.sh",
		"scan-remnawave-artifacts.sh subscription",
		"scan-remnawave-artifacts.sh proxy",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("integration Remnawave qualification lost %q", required)
		}
	}
	for _, forbidden := range []string{
		"environment: vpn-e2e",
		"${{ secrets.PODLAZ_E2E_PROFILE_URI }}",
		"${{ secrets.PODLAZ_E2E_PROFILE_URI_LIST }}",
		"${{ secrets.PODLAZ_E2E_EXPECTED_EGRESS_IP }}",
		"hosted-real-provider-tun.sh",
		"real-provider:",
		"real-provider-tun:",
		"pull_request_target:",
		"self-hosted",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("integration Remnawave qualification contains retired dependency %q", forbidden)
		}
	}
}
