package e2e_test

import (
	"os"
	"strings"
	"testing"
)

const (
	hostedResourceSoakScenario = "hosted-resource-soak.sh"
	hostedResourceSoakWorkflow = "../../.github/workflows/hosted-synthetic-tun.yml"
)

func readHostedResourceSoakFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireHostedResourceSoakMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted resource soak contract lost %q", marker)
		}
	}
}

func TestHostedResourceSoakPreservesCanonicalQ20MeasurementContract(t *testing.T) {
	script := readHostedResourceSoakFile(t, hostedResourceSoakScenario)

	requireHostedResourceSoakMarkers(t, script,
		"SOAK_DURATION_SECONDS=10800",
		"SOAK_PRECONDITION_WARMUP_SECONDS=30",
		"SOAK_WARMUP_SECONDS=120",
		"SOAK_SAMPLE_INTERVAL_SECONDS=60",
		"SOAK_DOCTOR_EVERY_SAMPLES=10",
		"SOAK_RECONNECT_WARMUP_SECONDS=120",
		"SOAK_RECONNECT_SAMPLES=3",
		"SOAK_CLEANUP_SETTLE_SECONDS=10",
		"TUN_HEALTH_TIMEOUT_SECONDS=75",
		"TUN_STATUS_TIMEOUT_SECONDS=10",
		"TUN_DIAGNOSTIC_TIMEOUT_SECONDS=90",
		"precondition_warmed_baseline",
		"run_active_measurement",
		"run_reconnect_measurement",
		"assert-replaced",
		"assert-gone",
		"tun-resource-soak-policy.json",
		"tun_soak_metrics.py",
		"inactive-baseline",
		"post-cleanup",
	)

	for _, forbidden := range []string{
		"mac80211_hwsim",
		"hostapd",
		"systemctl suspend",
		"PODLAZ_E2E_TUN_RECONCILIATION",
		"PODLAZ_E2E_TUN_HOOKS",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("hosted Q20 scenario duplicated out-of-scope lifecycle mechanic %q", forbidden)
		}
	}
}

func TestHostedResourceSoakUsesProductionLifecycleAndExactCleanupBeforeGuestDestruction(t *testing.T) {
	script := readHostedResourceSoakFile(t, hostedResourceSoakScenario)

	ordered := []string{
		"wait_for_control_ready candidate-ready",
		"precondition_warmed_baseline",
		"release_control candidate-ready",
		"wait_for_control_ready verified-active",
		"run_active_measurement",
		"release_control verified-active",
		"wait_for_control_ready terminal-clean",
		"capture_first_cleanup_boundary",
		"run_reconnect_measurement",
		"build_resource_report",
		"release_control terminal-clean",
		"wait_base_completion",
		"validate_base_positive_control",
	}
	previous := -1
	for _, marker := range ordered {
		index := strings.Index(script, marker)
		if index < 0 {
			t.Fatalf("hosted resource soak lost lifecycle boundary %q", marker)
		}
		if index <= previous {
			t.Fatalf("hosted resource soak lifecycle boundary %q is out of order", marker)
		}
		previous = index
	}

	requireHostedResourceSoakMarkers(t, script,
		"/usr/bin/podlaz connect --mode tun",
		"/usr/bin/podlaz disconnect",
		"wait_for_verified_cli_status",
		"capture_active_authority",
		"assert_privacy_envelope_present",
		"assert_foreign_sentinel",
		"verify_tun_package_resources_absent",
		"assert_clean_recovery_json_file",
		"PRODUCT_CLEANUP=pass",
		"ORDINARY_CONNECTIVITY=pass",
		"guest_exec timeout 20 getent ahostsv4 github.com",
		"guest_exec timeout 30 curl -4 -fsS --max-time 10 -o /dev/null https://api.ipify.org",
	)
}

func TestHostedResourceSoakPublicEvidenceIsBoundedAndPolicyTraceable(t *testing.T) {
	script := readHostedResourceSoakFile(t, hostedResourceSoakScenario)

	requireHostedResourceSoakMarkers(t, script,
		"hosted-resource-soak.json",
		"hosted-resource-soak-status.txt",
		"MAX_PUBLIC_BYTES=4194304",
		"MAX_PRIVATE_BYTES=6442450944",
		"HEAD:scripts/e2e/tun-resource-soak-policy.json",
		"policy.get(\"mode\") != \"observe\"",
		"report.get(\"verdict\") != \"observation_complete\"",
		"lifecycle.get(\"cleanup\", {}).get(\"ok\") is not True",
		"lifecycle.get(\"reconnect\", {}).get(\"ok\") is not True",
		"failure.class=",
		"failure.step=",
	)

	if strings.Contains(script, "PODLAZ_E2E_PROFILE_URI") || strings.Contains(script, "PODLAZ_E2E_PROFILE_LIST_URI") {
		t.Fatal("hosted resource soak must use the synthetic hosted profile rather than external profile secrets")
	}
}

func TestHostedResourceSoakWorkflowIsScheduledManualAndNotAnOrdinaryPRGate(t *testing.T) {
	workflow := readHostedResourceSoakFile(t, hostedResourceSoakWorkflow)

	requireHostedResourceSoakMarkers(t, workflow,
		"resource_soak:",
		"description: Run the full three-hour hosted resource-soak qualification",
		"github.event_name == 'schedule'",
		"github.event_name == 'workflow_dispatch' && inputs.resource_soak",
		"contains(github.event.pull_request.body, '<!-- run-hosted-resource-soak -->')",
		"resource-soak:",
		"runs-on: ubuntu-24.04",
		"timeout-minutes: 300",
		"SOAK_EXPECTED_RUNTIME_MINUTES: '210'",
		"SOAK_MIN_MEMORY_BYTES: '12884901888'",
		"SOAK_MIN_FREE_DISK_BYTES: '8589934592'",
		"SOAK_MAX_PUBLIC_BYTES: '4194304'",
		"bash scripts/e2e/hosted-resource-soak.sh \"${DEV_DEB}\"",
		"bash scripts/e2e/hosted-resource-soak.sh validate-report",
		"name: podlaz-hosted-resource-soak",
		"retention-days: 7",
	)

	resourceJob := strings.Index(workflow, "\n  resource-soak:\n")
	if resourceJob < 0 {
		t.Fatal("resource-soak job is missing")
	}
	resourceText := workflow[resourceJob:]
	if strings.Contains(resourceText, "github.event_name == 'pull_request' ||") {
		t.Fatal("resource soak became an ordinary pull-request gate")
	}
}

func TestHostedResourceSoakReusesExistingHostedControlBoundaries(t *testing.T) {
	script := readHostedResourceSoakFile(t, hostedResourceSoakScenario)

	requireHostedResourceSoakMarkers(t, script,
		"PODLAZ_E2E_HOSTED_CONTROL_PHASES=\"candidate-ready verified-active terminal-clean\"",
		"PODLAZ_E2E_HOSTED_CONTROL_TIMEOUT_SECONDS=15600",
		"candidate.provenance=pass",
		"tun.verified_active=pass",
		"tun.terminal_cleanup=pass",
		"guest.baseline_restored=pass",
		"outer.cleanup=pass",
	)
}
