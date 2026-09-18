package e2e_test

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	hostedPackageRestartWorkflow = "../../.github/workflows/hosted-package-restart.yml"
	hostedPackageRestartScenario = "hosted-package-restart-recovery.sh"
	hostedSyntheticTUNScenario   = "hosted-synthetic-tun.sh"
	profileInputHelper           = "lib/profile_input.sh"
	foreignStateHelper           = "lib/tun_foreign_state.sh"
	packageRestartScenario       = "tun-package-restart-recovery.sh"
)

func TestHostedPackageRestartUsesIsolatedRealProviderGuest(t *testing.T) {
	workflow := readRequiredFile(t, hostedPackageRestartWorkflow)
	for _, required := range []string{
		"name: Hosted Package Restart",
		"runs-on: ubuntu-24.04",
		"environment: vpn-e2e",
		"PODLAZ_E2E_PROFILE_URI: ${{ secrets.PODLAZ_E2E_PROFILE_URI }}",
		"PODLAZ_E2E_PROFILE_URI_LIST: ${{ secrets.PODLAZ_E2E_PROFILE_URI_LIST }}",
		"PODLAZ_E2E_EXPECTED_EGRESS_IP: ${{ secrets.PODLAZ_E2E_EXPECTED_EGRESS_IP }}",
		"PODLAZ_E2E_PUBLIC_IP_CHECK_URL: ${{ vars.PODLAZ_E2E_PUBLIC_IP_CHECK_URL }}",
		"podlaz_0.2.40_linux_amd64.deb",
		"c9d8f76838292d39355506123e2f03ca1f0a96227fb2c22af8324ac6baf3b278",
		"0.2.42+git.",
		"bash scripts/e2e/hosted-package-restart-recovery.sh",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("hosted package restart workflow lost %q", required)
		}
	}
	for _, forbidden := range []string{
		"runs-on: self-hosted",
		"PODLAZ_E2E_PROFILE_URI='<",
		"sudo bash scripts/e2e/tun-package-restart-recovery.sh",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("hosted package restart workflow contains forbidden marker %q", forbidden)
		}
	}
}

func TestHostedPackageRestartReusesHostedSyntheticGuestSubstrate(t *testing.T) {
	base := readRequiredFile(t, hostedSyntheticTUNScenario)
	if !strings.Contains(base, "if [[ \"${BASH_SOURCE[0]}\" == \"$0\" ]]; then") {
		t.Fatal("hosted synthetic TUN scenario is not source-safe")
	}
	if !strings.Contains(base, "git jq openssl") {
		t.Fatal("shared hosted system guest does not install git for exact package provenance")
	}

	scenario := readRequiredFile(t, hostedPackageRestartScenario)
	for _, required := range []string{
		"source \"${SCRIPT_DIR}/hosted-synthetic-tun.sh\"",
		"prepare_system_guest",
		"start_system_guest",
		"install_tun_authorization",
		"install_package_restart_recovery_authorization",
		`action.id == "io.github.aidarkhusainov.podlaz.recover-execute"`,
		"guest_exec",
		"capture_outer_baseline",
		"assert_outer_baseline_restored",
		"tun-package-restart-recovery.sh",
		"PODLAZ_E2E_PROFILE_URI_FILE",
		`FOREIGN_NFT_TABLE="${FOREIGN_NFT_TABLE}"`,
		`GUEST_PREVIOUS="${GUEST_RUN_ROOT}/podlaz-v0.2.40.deb"`,
		`guest_exec install -o e2e -g e2e -m 0600 /run/podlaz-synthetic-xray/podlaz-v0.2.40.deb "${GUEST_PREVIOUS}"`,
		"redaction_scan.py",
		`GUEST_FOCUSED_PUBLIC="/opt/podlaz-hosted-package-restart-public"`,
		"package-restart-result.txt",
		`$1=="candidate_outcome"`,
		"traffic_v0.2.40-package-restart-vpn=passed",
		"package_restart_second_recovery_clean=true",
		`local source="${GUEST_ROOT}${GUEST_FOCUSED_PUBLIC}"`,
	} {
		if !strings.Contains(scenario, required) {
			t.Fatalf("hosted package restart scenario lost %q", required)
		}
	}
	if got := strings.Count(scenario, `FOREIGN_NFT_TABLE="${FOREIGN_NFT_TABLE}"`); got != 2 {
		t.Fatalf("hosted package restart must hand the v0.2.40-safe foreign nft sentinel to both focused branches, got %d", got)
	}

	for _, forbidden := range []string{
		`GUEST_FOCUSED_PUBLIC="${GUEST_RUN_ROOT}/public"`,
		"GUEST_FOCUSED_EXPORT",
		`GUEST_PREVIOUS="/run/podlaz-synthetic-xray/podlaz-v0.2.40.deb"`,
		"systemctl restart podlazd.service",
		"recover --execute --yes",
		"nft flush",
		"ip route flush",
		"ip rule flush",
	} {
		if strings.Contains(scenario, forbidden) {
			t.Fatalf("hosted controller must not repair product state itself: %q", forbidden)
		}
	}
}

func TestHostedPackageRestartForeignSentinelIsV0240AllocationCompatible(t *testing.T) {
	helper := readRequiredFile(t, foreignStateHelper)
	match := regexp.MustCompile(`FOREIGN_RULE_PRIORITY:=([0-9]+)`).FindStringSubmatch(helper)
	if len(match) != 2 {
		t.Fatal("foreign-state helper does not expose a numeric default policy-rule priority")
	}
	priority, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatalf("parse foreign policy-rule priority: %v", err)
	}
	if priority <= 10000 || priority >= 32766 {
		t.Fatalf("foreign policy-rule priority %d must stay above Podlaz session candidates and below the v0.2.40 reserved kernel boundary", priority)
	}
}

func TestPackageRestartSupportsPrivateProfileFileAndExpectedEgress(t *testing.T) {
	profile := readRequiredFile(t, profileInputHelper)
	if !strings.Contains(profile, "PODLAZ_E2E_PROFILE_URI_FILE") {
		t.Fatal("profile input helper lacks private file input")
	}

	scenario := readRequiredFile(t, packageRestartScenario)
	for _, required := range []string{
		"PODLAZ_E2E_PROFILE_URI_FILE",
		"PODLAZ_E2E_EXPECTED_EGRESS_IP",
		"PODLAZ_E2E_REQUIRE_EGRESS_CHANGE",
		"PODLAZ_E2E_PUBLIC_IP_CHECK_URL",
		"check_expected_egress",
		"start_source_connect_observer",
		"stop_source_connect_observer",
		"capture_source_connect_observation",
		"capture_connect_failure_evidence",
		"connect_v0.2.40_failure_class=",
		"connect_v0.2.40_phase=",
		"connect_v0.2.40_rollback_status=",
		"connect_v0.2.40_daemon_classification=",
		"connect_v0.2.40_tun_primary_classification=",
		"connect_v0.2.40_report_status=",
		"connect_v0.2.40_report_failure_phase=",
		"connect_v0.2.40_report_rollback_status=",
		"connect_v0.2.40_report_network_apply=",
		"connect_v0.2.40_observer_transaction=",
		"connect_v0.2.40_observer_applied_steps=",
		"connect_v0.2.40_observer_health=",
		"connect_v0.2.40_observer_tun_link=",
		"connect_v0.2.40_observer_desired_dns=",
		"connect_v0.2.40_observer_desired_nftables=",
		"connect_v0.2.40_preflight=",
		"connect_v0.2.40_network_apply=",
		"check_https_and_dns v0.2.40-package-restart-vpn",
		"check_expected_egress v0.2.40-package-restart-vpn",
		"check_https_and_dns package-restart-resumed-vpn",
		"check_expected_egress package-restart-resumed-vpn",
	} {
		if !strings.Contains(scenario, required) {
			t.Fatalf("package restart acceptance lost %q", required)
		}
	}
}

func readRequiredFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
