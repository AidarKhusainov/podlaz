package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const (
	hostedResourceSoakWorkflow = "../../.github/workflows/hosted-resource-soak.yml"
	hostedResourceSoakScript   = "hosted-resource-soak.sh"
	tunResourceSoakScript      = "tun-resource-soak.sh"
	tunResourceSoakPolicy      = "tun-resource-soak-policy.json"
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
			t.Fatalf("hosted resource-soak contract lost %q", marker)
		}
	}
}

func forbidHostedResourceSoakMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			t.Fatalf("hosted resource-soak contract contains forbidden %q", marker)
		}
	}
}

func TestHostedResourceSoakWorkflowIsLongRunningDisposableQualification(t *testing.T) {
	workflow := readHostedResourceSoakFile(t, hostedResourceSoakWorkflow)
	requireHostedResourceSoakMarkers(t, workflow,
		"name: Hosted Resource Soak", "workflow_dispatch:", "schedule:", "cron: '37 3 * * 0'",
		"runs-on: ubuntu-24.04", "timeout-minutes: 330",
		"MIN_FREE_DISK_MIB: 6144", "MIN_AVAILABLE_MEMORY_MIB: 8192",
		"MAX_PUBLIC_ARTIFACT_BYTES: 524288", "bash scripts/build-deb.sh",
		"bash scripts/e2e/hosted-resource-soak.sh", "retention-days: 7",
	)
	for _, action := range []string{"actions/checkout", "actions/setup-go", "actions/upload-artifact"} {
		re := regexp.MustCompile(regexp.QuoteMeta("uses: "+action+"@") + "[0-9a-f]{40}")
		if !re.MatchString(workflow) {
			t.Fatalf("%s must be pinned to an immutable commit", action)
		}
	}
	forbidHostedResourceSoakMarkers(t, workflow,
		"pull_request:", "self-hosted", "${{ secrets.",
		"PODLAZ_E2E_SOAK_DURATION_SECONDS", "PODLAZ_E2E_SOAK_WARMUP_SECONDS",
		"PODLAZ_E2E_SOAK_SAMPLE_INTERVAL_SECONDS",
	)
}

func TestHostedResourceSoakScenarioReusesExistingMeasurementEngine(t *testing.T) {
	script := readHostedResourceSoakFile(t, hostedResourceSoakScript)
	requireHostedResourceSoakMarkers(t, script,
		"hosted-synthetic-tun.sh", "prepare_system_guest", "install_tun_authorization", "start_system_guest",
		"start_synthetic_xray_endpoint", "provision_trusted_host",
		"isolation.collect_snapshot()", "isolation.validate_clean_baseline(",
		"uplink_environment=\"hosted-guest\"", "PODLAZ_E2E_PREBUILT_DEB", "PODLAZ_E2E_CANDIDATE_COMMIT",
		"PODLAZ_E2E_SOAK_UPLINK_ENVIRONMENT=hosted-guest", "bash /workspace/scripts/e2e/tun-resource-soak.sh",
		".configuration.duration_seconds == 10800", ".configuration.warmup_seconds == 120",
		".configuration.sample_interval_seconds == 60", ".configuration.reconnect_samples == 3",
		".lifecycle.cleanup.ok == true", ".lifecycle.reconnect.ok == true",
		"assert_guest_network_baseline_restored", "guest.ordinary_connectivity_restored",
		"assert_outer_baseline_restored",
	)
	forbidHostedResourceSoakMarkers(t, script,
		"PODLAZ_E2E_SOAK_DURATION_SECONDS=", "PODLAZ_E2E_SOAK_WARMUP_SECONDS=",
		"PODLAZ_E2E_SOAK_SAMPLE_INTERVAL_SECONDS=", "PODLAZ_E2E_TUN_HOOK",
		"PODLAZ_E2E_TUN_RECONCILIATION", "systemctl restart podlazd.service", "ip link del podlaz0",
	)
	cmd := exec.Command("bash", "-n", hostedResourceSoakScript)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedResourceSoakScript, err, output)
	}
}

func TestTunResourceSoakPrebuiltCandidatePreservesExactProvenance(t *testing.T) {
	script := readHostedResourceSoakFile(t, tunResourceSoakScript)
	requireHostedResourceSoakMarkers(t, script,
		"PODLAZ_E2E_PREBUILT_DEB", "PODLAZ_E2E_CANDIDATE_COMMIT",
		"prebuilt resource-soak candidate requires an exact 40-hex commit",
		"BUILD_COMMIT=\"${PODLAZ_E2E_CANDIDATE_COMMIT,,}\"",
		"DEV_DEB=\"$(readlink -f -- \"${DEV_DEB}\")\"",
		"sudo -n apt install -y \"${DEV_DEB}\"", "verify_package_provenance",
	)
	cmd := exec.Command("bash", "-n", tunResourceSoakScript)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", tunResourceSoakScript, err, output)
	}
}

func TestCheckedInResourceSoakPolicyRemainsReviewedContract(t *testing.T) {
	data, err := os.ReadFile(tunResourceSoakPolicy)
	if err != nil {
		t.Fatal(err)
	}
	var policy map[string]any
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatal(err)
	}
	if policy["schema_version"] != float64(2) || policy["mode"] != "observe" {
		t.Fatalf("unexpected soak policy identity: %#v", policy)
	}
	metricLimits, ok := policy["metric_limits"].(map[string]any)
	if !ok || len(metricLimits) != 0 {
		t.Fatal("reviewed observe policy unexpectedly gained active-trend limits")
	}
	lifecycle, ok := policy["lifecycle_limits"].(map[string]any)
	if !ok {
		t.Fatal("checked-in soak policy lost lifecycle thresholds")
	}
	for _, phase := range []string{"cleanup", "reconnect"} {
		rules, ok := lifecycle[phase].(map[string]any)
		if !ok || len(rules) == 0 {
			t.Fatalf("checked-in soak policy lost %s thresholds", phase)
		}
	}
}
