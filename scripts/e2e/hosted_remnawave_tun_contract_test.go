package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
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
		"artifact.privacy",
		"assert_public_artifact_privacy || return 1",
		"failure\\.class=(?:none|product|remnawave|fixture|infrastructure|capability|diagnostic_unknown)",
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
		"failure\\\\.class=",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("ephemeral Remnawave TUN scenario contains retired provider assumption %q", forbidden)
		}
	}
}

func TestHostedRemnawaveTUNFinalScannerRejectsRawPublicData(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "hosted-remnawave-tun.txt")
	lines := []string{
		"candidate.commit=" + strings.Repeat("a", 40),
		"candidate.package_sha256=" + strings.Repeat("b", 64),
		"candidate.provenance=pass",
		"remnawave.material_private=pass",
		"ordinary_user.boundary=pass",
		"tun.verified_active=pass",
		"tun.system_dns=pass",
		"tun.ipv4_tcp=pass",
		"tun.tls=pass",
		"tun.https=pass",
		"tun.remnawave_path=pass",
		"tun.doctor=observed",
		"privacy.direct_uplink_blocked=pass",
		"foreign.state_preserved=pass",
		"tun.clean_disconnect=pass",
		"tun.terminal_cleanup=pass",
		"tun.recovery_clean=pass",
		"guest.baseline_restored=pass",
		"guest.ordinary_connectivity_restored=pass",
		"outer.cleanup=pass",
		"artifact.privacy=pass",
		"fixture.cleanup=pass",
		"failure.class=none",
		"failure.step=none",
	}
	if err := os.WriteFile(report, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func() error {
		cmd := exec.Command("bash", "hosted-remnawave-tun.sh", "validate-report")
		cmd.Env = append(os.Environ(),
			"E2E_ARTIFACT_DIR="+dir,
			"PODLAZ_E2E_CANDIDATE_COMMIT="+strings.Repeat("a", 40),
			"PODLAZ_E2E_CANDIDATE_SHA256="+strings.Repeat("b", 64),
		)
		return cmd.Run()
	}
	if err := run(); err != nil {
		t.Fatalf("normalized final TUN report rejected: %v", err)
	}
	if err := os.WriteFile(report, []byte(strings.Join(append(lines, "raw.provider=secret"), "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil {
		t.Fatal("final TUN scanner accepted non-normalized public data")
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
