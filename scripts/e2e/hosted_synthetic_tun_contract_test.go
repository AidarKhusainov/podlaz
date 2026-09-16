package e2e_test

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const (
	hostedSyntheticTUNWorkflow = "../../.github/workflows/hosted-synthetic-tun.yml"
	hostedSyntheticTUNScript   = "hosted-synthetic-tun.sh"
)

func readHostedSyntheticTUNFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireHostedSyntheticTUNMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted synthetic TUN contract lost %q", marker)
		}
	}
}

func forbidHostedSyntheticTUNMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			t.Fatalf("hosted synthetic TUN contract contains forbidden %q", marker)
		}
	}
}

func TestHostedSyntheticTUNWorkflowIsThinPermanentQualification(t *testing.T) {
	workflow := readHostedSyntheticTUNFile(t, hostedSyntheticTUNWorkflow)

	requireHostedSyntheticTUNMarkers(t, workflow,
		"name: Hosted Synthetic TUN",
		"pull_request:",
		"workflow_dispatch:",
		"schedule:",
		"runs-on: ubuntu-24.04",
		"persist-credentials: false",
		"bash scripts/build-deb.sh",
		"bash scripts/e2e/hosted-synthetic-tun.sh",
		"hosted-synthetic-tun.txt",
	)

	for _, action := range []string{"actions/checkout", "actions/setup-go", "actions/upload-artifact"} {
		re := regexp.MustCompile(regexp.QuoteMeta("uses: "+action+"@") + `[0-9a-f]{40}`)
		if !re.MatchString(workflow) {
			t.Fatalf("%s must be pinned to an immutable 40-hex commit", action)
		}
	}

	forbidHostedSyntheticTUNMarkers(t, workflow,
		"self-hosted",
		"${{ secrets.",
		"PODLAZ_E2E_PROFILE_URI",
		"podlaz connect --mode tun",
		"machinectl ",
		"systemd-nspawn ",
		"iptables ",
		"nft ",
		"qemu-",
		"hosted-e2e-capability",
	)
}

func TestHostedSyntheticTUNCandidateProvenanceMatchesCheckedOutCandidate(t *testing.T) {
	workflow := readHostedSyntheticTUNFile(t, hostedSyntheticTUNWorkflow)
	requireHostedSyntheticTUNMarkers(t, workflow,
		"PODLAZ_COMMIT: ${{ github.sha }}",
	)
	forbidHostedSyntheticTUNMarkers(t, workflow,
		"CANDIDATE_COMMIT:",
		"ref: ${{ env.CANDIDATE_COMMIT }}",
		"PODLAZ_E2E_CANDIDATE_COMMIT:",
	)
}

func TestHostedSyntheticTUNScenarioOwnsCanonicalLifecycle(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)

	requireHostedSyntheticTUNMarkers(t, script,
		"validate_candidate()",
		"capture_outer_baseline()",
		"prepare_system_guest()",
		"start_system_guest()",
		"install_candidate_in_guest()",
		"assert_guest_package_provenance()",
		"start_synthetic_xray_endpoint()",
		"install_tun_authorization()",
		"assert_ordinary_user_boundary()",
		"capture_guest_network_baseline()",
		"assert_verified_active_authority()",
		"run_active_traffic_checks()",
		"run_tun_doctor()",
		"assert_terminal_authority_clean()",
		"assert_guest_network_baseline_restored()",
		"assert_clean_recovery_json_file",
		"assert_public_artifact_privacy()",
		"cleanup_outer_plumbing()",
		"/dev/net/tun",
		"settings\": {\"clients\"",
	)

	forbidHostedSyntheticTUNMarkers(t, script,
		"probe_qemu_",
		"qemu-system",
		"/proc/sys/kernel/random/boot_id",
		"capture_pid=",
		"argv_capture_pid=",
		"transport_capture_pid=",
		"connect_capture_pid=",
		"runuser -u e2e -g podlaz",
		"PODLAZ_E2E_PROFILE_URI",
	)
	cmd := exec.Command("bash", "-n", hostedSyntheticTUNScript)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedSyntheticTUNScript, err, output)
	}
}

func TestHostedSyntheticTUNEndpointIsRoutedBehindGuestGateway(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	requireHostedSyntheticTUNMarkers(t, script,
		`HOST_ENDPOINT_DEV="pzsyntsrv"`,
		`ENDPOINT_CIDR="172.31.253.1/32"`,
		`ENDPOINT_IP="172.31.253.1"`,
		`ip link add dev "${HOST_ENDPOINT_DEV}" type dummy`,
		`ip addr add "${ENDPOINT_CIDR}" dev "${HOST_ENDPOINT_DEV}"`,
		`ip link del dev "${HOST_ENDPOINT_DEV}"`,
		`"${ENDPOINT_IP}:${port}"`,
	)
	forbidHostedSyntheticTUNMarkers(t, script,
		`"${HOST_IP}:${port}"`,
		`@${HOST_IP}:`,
	)
}

func TestHostedSyntheticTUNEvidenceSchemaIsBounded(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	for _, key := range []string{
		"candidate.provenance",
		"ordinary_user.boundary",
		"tun.verified_active",
		"tun.system_dns",
		"tun.https_tls",
		"tun.doctor",
		"tun.clean_disconnect",
		"tun.terminal_cleanup",
		"tun.recovery_clean",
		"guest.baseline_restored",
		"outer.cleanup",
		"artifact.privacy",
	} {
		if !strings.Contains(script, key) {
			t.Fatalf("hosted synthetic TUN evidence schema lost %q", key)
		}
	}
	forbidHostedSyntheticTUNMarkers(t, script,
		"kernel.netns",
		"qemu.kvm",
		"qemu.tcg",
		"qemu.reboot",
	)
}

func TestHostedSyntheticTUNPolkitFixtureIsNarrow(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	start := strings.Index(script, "install_tun_authorization() {")
	end := strings.Index(script, "\nrun_guest_user() {")
	if start < 0 || end <= start {
		t.Fatal("TUN authorization function boundaries not found")
	}
	rule := script[start:end]
	requireHostedSyntheticTUNMarkers(t, rule,
		"io.github.aidarkhusainov.podlaz.connect-tun",
		"io.github.aidarkhusainov.podlaz.disconnect",
	)
	forbidHostedSyntheticTUNMarkers(t, rule,
		"io.github.aidarkhusainov.podlaz.connect-proxy-only",
		"action.id.indexOf(\"io.github.aidarkhusainov.podlaz.\")",
	)
}
