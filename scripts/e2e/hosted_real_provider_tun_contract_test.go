package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostedRealProviderTUNReusesIsolatedSubstrateAndKeepsSecretsPrivate(t *testing.T) {
	data, err := os.ReadFile("hosted-real-provider-tun.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		`source "${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		"assert_guest_package_provenance",
		"assert_ordinary_user_boundary",
		"machinectl copy-to",
		"chmod 0600",
		`guest_exec rm -rf "${GUEST_PROVIDER_DIR}"`,
		"wait_guest_status verified-active",
		"assert_verified_active_authority",
		"getent ahostsv4 example.com",
		"/dev/tcp/${PROBE_IP}/443",
		"openssl s_client",
		"curl -4 -fsS -o /dev/null https://example.com/",
		`[[ "${ACTIVE_EGRESS}" != "${ORDINARY_EGRESS}" ]]`,
		"--interface \"${GUEST_IF}\"",
		"run_tun_doctor",
		"assert_terminal_authority_clean",
		"run_clean_recovery",
		"assert_guest_network_baseline_restored",
		"assert_ordinary_connectivity_restored",
		"remove_guest_private_state",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("trusted provider TUN scenario lost contract %q", required)
		}
	}
	if strings.Contains(text, "self-hosted") {
		t.Fatal("trusted provider TUN must use the permanent hosted guest substrate")
	}
}

func TestHostedRealProviderTUNWorkflowKeepsProxyAndTUNSignalsSeparate(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/integration.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"- cron: '17 3 * * *'",
		"- cron: '47 4 * * 0'",
		"real_provider_tun:",
		"real-provider:\n    name: Real proxy data plane",
		"real-provider-tun:\n    name: Real provider full-TUN",
		"needs: real-provider",
		"environment: vpn-e2e",
		"github.ref == 'refs/heads/master'",
		"inputs.real_provider_tun",
		"PODLAZ_E2E_PROFILE_URI: ${{ secrets.PODLAZ_E2E_PROFILE_URI }}",
		"PODLAZ_E2E_PROFILE_URI_LIST: ${{ secrets.PODLAZ_E2E_PROFILE_URI_LIST }}",
		"PODLAZ_E2E_EXPECTED_EGRESS_IP: ${{ secrets.PODLAZ_E2E_EXPECTED_EGRESS_IP }}",
		"bash scripts/e2e/hosted-real-provider-tun.sh",
		"bash scripts/e2e/scan-public-artifacts.sh real-provider-tun",
		"steps.provider_tun_scan.outcome == 'success'",
		"steps.provider_tun_private_cleanup.outcome == 'success'",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("integration provider qualification lost %q", required)
		}
	}
	jobStart := strings.Index(text, "\n  real-provider-tun:")
	if jobStart < 0 {
		t.Fatal("real-provider TUN job is missing")
	}
	job := text[jobStart:]
	runStep := strings.Index(job, "      - name: Run isolated real-provider TUN qualification")
	if runStep < 0 {
		t.Fatal("real-provider TUN runtime step is missing")
	}
	preRuntime := job[:runStep]
	for _, secretRef := range []string{
		"PODLAZ_E2E_PROFILE_URI: ${{ secrets.PODLAZ_E2E_PROFILE_URI }}",
		"PODLAZ_E2E_PROFILE_URI_LIST: ${{ secrets.PODLAZ_E2E_PROFILE_URI_LIST }}",
		"PODLAZ_E2E_EXPECTED_EGRESS_IP: ${{ secrets.PODLAZ_E2E_EXPECTED_EGRESS_IP }}",
	} {
		if strings.Contains(preRuntime, secretRef) {
			t.Fatalf("provider TUN secret %q must be scoped to the runtime step", secretRef)
		}
	}
	for _, forbidden := range []string{"pull_request_target:", "self-hosted"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("trusted provider workflow must not contain %q", forbidden)
		}
	}
}

func TestHostedRealProviderTUNPublicScannerRejectsNonNormalizedEvidence(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "hosted-real-provider-tun.txt")
	keys := []string{
		"candidate.provenance",
		"provider.material_private",
		"ordinary_user.boundary",
		"tun.verified_active",
		"tun.system_dns",
		"tun.ipv4_tcp",
		"tun.tls",
		"tun.https",
		"tun.provider_egress",
		"tun.doctor",
		"privacy.direct_uplink_blocked",
		"foreign.state_preserved",
		"tun.clean_disconnect",
		"tun.terminal_cleanup",
		"tun.recovery_clean",
		"guest.baseline_restored",
		"guest.ordinary_connectivity_restored",
		"outer.cleanup",
		"artifact.privacy",
	}
	var safe strings.Builder
	safe.WriteString("candidate.commit=" + strings.Repeat("a", 40) + "\n")
	safe.WriteString("candidate.package_sha256=" + strings.Repeat("b", 64) + "\n")
	for _, key := range keys {
		safe.WriteString(key + "=pass\n")
	}
	safe.WriteString("failure.class=none\nfailure.step=none\n")
	if err := os.WriteFile(report, []byte(safe.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func() error {
		cmd := exec.Command("bash", "scan-public-artifacts.sh", "real-provider-tun")
		cmd.Env = append(os.Environ(), "E2E_ARTIFACT_DIR="+dir)
		return cmd.Run()
	}
	if err := run(); err != nil {
		t.Fatalf("normalized provider TUN report was rejected: %v", err)
	}
	if err := os.WriteFile(report, []byte(safe.String()+"unexpected.raw=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil {
		t.Fatal("provider TUN scanner accepted non-normalized evidence")
	}
}
