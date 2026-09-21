package e2e_test

import (
	"strings"
	"testing"
)

const (
	hostedPackageHistoryWorkflow = "../../.github/workflows/hosted-package-history.yml"
	hostedMaintainedUpgrade      = "hosted-maintained-package-upgrade.sh"
	hostedV029Recovery           = "hosted-v029-package-recovery.sh"
)

func TestHostedPackageHistorySeparatesMaintainedAndPinnedRuntimeBoundaries(t *testing.T) {
	workflow := readRequiredFile(t, hostedPackageHistoryWorkflow)
	for _, required := range []string{
		"name: Hosted Package History",
		"name: Maintained previous-release upgrade",
		"name: Pinned v0.2.29 legacy recovery",
		"runs-on: ubuntu-24.04",
		"MAINTAINED_RELEASE_VERSION: 0.2.42",
		"MAINTAINED_RELEASE_COMMIT: 6e933a9f1b471c83ba884ee1f0569286f1dafdce",
		"MAINTAINED_RELEASE_SHA256: 62acca173c0618ef3c13c7bdd810a128d42fd8c9df38d951dfb314167e2659da",
		"V029_RELEASE_COMMIT: c846f5465a90a50d72f3fc393d639a402d590798",
		"V029_RELEASE_SHA256: 91644dee9ca92ddc5c48793b926f20d18da4d4267cbfdd3b41303e1e5c52516e",
		"hosted-maintained-package-upgrade.sh",
		"hosted-v029-package-recovery.sh",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("hosted package history workflow lost %q", required)
		}
	}
	if strings.Count(workflow, "name: Maintained previous-release upgrade") != 1 ||
		strings.Count(workflow, "name: Pinned v0.2.29 legacy recovery") != 1 {
		t.Fatal("maintained and pinned historical package boundaries must remain independently diagnosable jobs")
	}
}

func TestHostedMaintainedUpgradeUsesOneNormalCandidateReplacement(t *testing.T) {
	script := readRequiredFile(t, hostedMaintainedUpgrade)
	for _, required := range []string{
		"hosted-synthetic-tun.sh",
		"install_previous_fixture",
		"install_candidate_once",
		"capture_network_session_id",
		"start_privacy_watch",
		"stop_privacy_watch",
		"assert_verified_active_authority",
		"run_active_traffic_checks",
		"create_foreign_sentinel",
		"assert_foreign_sentinel",
		"assert_terminal_authority_clean",
		"assert_guest_network_baseline_restored",
		"run_clean_recovery",
		"assert_exact_podlaz_package_runtime_provenance",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("maintained package upgrade scenario lost %q", required)
		}
	}
	install := shellFunctionBody(t, script, "install_candidate_once")
	if strings.Count(install, "apt-get install") != 1 {
		t.Fatal("maintained package upgrade must perform exactly one candidate package install")
	}
	for _, forbidden := range []string{
		"systemctl start",
		"systemctl restart",
		"systemctl daemon-reload",
		"recover --execute",
		"podlaz connect",
	} {
		if strings.Contains(install, forbidden) {
			t.Fatalf("candidate package replacement must not repair product state with %q", forbidden)
		}
	}
	if strings.Count(script, "/usr/bin/podlaz connect --mode tun") != 1 {
		t.Fatal("maintained package upgrade must issue exactly one CLI connect on the lower release")
	}
	for _, required := range []string{
		"session_before",
		"session_after",
		"maintained.same_network_session",
		"maintained.privacy_continuous",
		"maintained.post_upgrade_traffic",
		"maintained.foreign_state",
		"maintained.terminal_cleanup",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("maintained upgrade continuity proof lost %q", required)
		}
	}
}

func TestHostedV029RecoveryRunsPinnedLegacyHarnessWithoutChangingItsSemantics(t *testing.T) {
	script := readRequiredFile(t, hostedV029Recovery)
	for _, required := range []string{
		"hosted-synthetic-tun.sh",
		"network-recovery-package-acceptance.sh",
		"PODLAZ_E2E_HISTORICAL_UPGRADE_ONLY=true",
		"PODLAZ_E2E_BASE_VERSION=v0.2.29",
		"podlaz_0.0.0~dev-1_linux_amd64.deb",
		"assert_guest_package_provenance",
		"create_foreign_sentinel",
		"assert_foreign_sentinel",
		"capture_guest_network_baseline",
		"assert_guest_network_baseline_restored",
		"legacy_upgrade_reconstructed_current_boot_session",
		"candidate_package_replaced_daemon",
		"network_recovery_acceptance_complete",
		"pinned_v029.runtime_provenance",
		"pinned_v029.foreign_state",
		"pinned_v029.network_restored",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("pinned v0.2.29 hosted recovery lost %q", required)
		}
	}
	if strings.Contains(script, "same_network_session") {
		t.Fatal("v0.2.29 predates Network Session IDs; pinned legacy recovery must prove reconstruction, not ID continuity")
	}
}

func TestHostedPackageHistoryDoesNotReuseV0240RegressionAsEitherBoundary(t *testing.T) {
	workflow := readRequiredFile(t, hostedPackageHistoryWorkflow)
	if strings.Contains(workflow, "v0.2.40") || strings.Contains(workflow, "podlaz_0.2.40") {
		t.Fatal("Q19/Q32 package history workflow must not substitute the separate v0.2.40 package-restart regression")
	}
}

func TestPinnedHistoryEvidenceKeyRemainsNormalized(t *testing.T) {
	script := readRequiredFile(t, "network-recovery-package-scenario.sh")
	if !strings.Contains(script, "write_evidence candidate_package_transition_result_success") {
		t.Fatal("historical package transition evidence must use a normalized evidence key")
	}
	if strings.Contains(script, "write_evidence \"candidate_package_transition_result_success ") {
		t.Fatal("historical package transition evidence key must not contain diagnostic fields")
	}
}

func TestPinnedHistoryUsesFocusedHistoricalBoundaryWithoutChangingLegacyDefault(t *testing.T) {
	script := readRequiredFile(t, "network-recovery-package-scenario.sh")
	for _, required := range []string{
		`PODLAZ_E2E_HISTORICAL_UPGRADE_ONLY:=false`,
		`if [[ "${PODLAZ_E2E_HISTORICAL_UPGRADE_ONLY}" == true ]]`,
		"run_historical_upgrade_terminal",
		"historical_upgrade_terminal_cleanup",
		"historical_upgrade_recovery_clean",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("focused historical package boundary lost %q", required)
		}
	}
	focus := strings.Index(script, `if [[ "${PODLAZ_E2E_HISTORICAL_UPGRADE_ONLY}" == true ]]`)
	generic := strings.Index(script, "\nforce_kill_inside_durable_rollback\n")
	if focus < 0 || generic < 0 || focus > generic {
		t.Fatal("historical-only branch must terminate before generic restart/rollback exercises")
	}
}
