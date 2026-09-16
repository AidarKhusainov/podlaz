package e2e_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	hostedCapabilityWorkflow = "../../.github/workflows/hosted-e2e-capability.yml"
	hostedCapabilityScript   = "hosted-e2e-capability.sh"
)

func readHostedCapabilityFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireHostedCapabilityMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted capability contract lost %q", marker)
		}
	}
}

func forbidHostedCapabilityMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			t.Fatalf("hosted capability contract contains forbidden %q", marker)
		}
	}
}

func TestHostedE2ECapabilityWorkflowContract(t *testing.T) {
	workflow := readHostedCapabilityFile(t, hostedCapabilityWorkflow)

	requireHostedCapabilityMarkers(t, workflow,
		"name: Hosted E2E Capability",
		"pull_request:",
		"workflow_dispatch:",
		"permissions:\n  contents: read",
		"runs-on: ubuntu-24.04",
		"timeout-minutes: 120",
		"persist-credentials: false",
		"bash scripts/e2e/hosted-e2e-capability.sh",
		"hosted-e2e-capability.txt",
	)

	for _, action := range []string{"actions/checkout", "actions/setup-go", "actions/upload-artifact"} {
		re := regexp.MustCompile(regexp.QuoteMeta("uses: "+action+"@") + `[0-9a-f]{40}`)
		if !re.MatchString(workflow) {
			t.Fatalf("%s must be pinned to an immutable 40-hex commit", action)
		}
	}

	forbidHostedCapabilityMarkers(t, workflow,
		"self-hosted",
		"environment: vpn-e2e",
		"${{ secrets.",
		"PODLAZ_E2E_PROFILE_URI",
		"PODLAZ_E2E_PROFILE_URI_LIST",
		"PODLAZ_E2E_EXPECTED_EGRESS_IP",
		"podlaz connect --mode tun",
	)
}

func TestHostedE2ECapabilityScriptContract(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)

	requireHostedCapabilityMarkers(t, script,
		"PODLAZ_E2E_CAPABILITY_SOURCE_ONLY",
		"record_capability()",
		"capture_outer_baseline()",
		"assert_outer_control_plane_healthy()",
		"cleanup_outer_plumbing()",
		"teardown_all()",
		"probe_hosted_kernel_primitives()",
		"prepare_system_guest()",
		"start_system_guest()",
		"guest_exec()",
		"install_candidate_in_guest()",
		"assert_guest_package_provenance()",
		"run_guest_ordinary_user_acceptance()",
		"start_synthetic_xray_endpoint()",
		"install_tun_ci_authorization()",
		"compare_synthetic_identity_chain()",
		"run_synthetic_tun_lifecycle()",
		"assert_guest_tun_clean()",
		"probe_qemu_accelerators()",
		"prepare_qemu_image()",
		"start_qemu_guest()",
		"wait_qemu_ssh()",
		"reboot_qemu_guest()",
		"stop_qemu_guest()",
		"validate-report",
		"/dev/net/tun",
		"systemd-nspawn",
		"NetworkManager",
		"systemd-resolved",
		"doctor --tun",
		"/proc/sys/kernel/random/boot_id",
		"cloud-images.ubuntu.com/releases/noble",
		"SHA256SUMS",
		"127.0.0.1",
	)

	forbidHostedCapabilityMarkers(t, script,
		"PODLAZ_E2E_PROFILE_URI",
		"PODLAZ_E2E_PROFILE_URI_LIST",
		"PODLAZ_E2E_EXPECTED_EGRESS_IP",
	)

	cmd := exec.Command("bash", "-n", hostedCapabilityScript)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedCapabilityScript, err, output)
	}
}

func TestHostedE2ECapabilitySyntheticVLESSServerUsesPackagedInboundSchema(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)
	start := strings.Index(script, "start_synthetic_xray_endpoint() {")
	end := strings.Index(script, "\ninstall_tun_ci_authorization() {")
	if start < 0 || end <= start {
		t.Fatal("synthetic Xray endpoint function boundaries not found")
	}
	fixture := script[start:end]
	if !strings.Contains(fixture, "\"settings\": {\"clients\": [{\"id\": \"${uuid}\"}], \"decryption\": \"none\"}") {
		t.Fatal("synthetic VLESS inbound must configure the packaged Xray clients field")
	}
	if strings.Contains(fixture, "\"settings\": {\"users\":") {
		t.Fatal("synthetic VLESS inbound must not use outbound-only users field")
	}
}

func TestHostedE2ECapabilityEvidenceSchema(t *testing.T) {
	script := readHostedCapabilityFile(t, hostedCapabilityScript)

	keys := []string{
		"outer.baseline",
		"outer.control_plane.before",
		"kernel.tun",
		"kernel.netns",
		"kernel.route_rule",
		"kernel.nftables",
		"guest.systemd",
		"guest.resolved",
		"guest.networkmanager",
		"guest.uplink",
		"guest.internet.before",
		"package.installed",
		"package.runtime_provenance",
		"authorization.ordinary_user",
		"authorization.polkit",
		"proxy.lifecycle",
		"tun.verified_active",
		"tun.system_dns",
		"tun.https_tls",
		"tun.doctor",
		"tun.ipv6",
		"tun.pmtu",
		"tun.networkmanager_postcondition",
		"tun.clean_disconnect",
		"tun.recovery_clean",
		"artifact.privacy",
		"qemu.available",
		"qemu.kvm_present",
		"qemu.kvm_usable",
		"qemu.tcg_usable",
		"qemu.image_checksum",
		"qemu.disk_budget",
		"qemu.boot",
		"qemu.reboot_boot_id",
		"outer.control_plane.after",
		"outer.cleanup",
	}
	for _, key := range keys {
		if !strings.Contains(script, key) {
			t.Fatalf("hosted capability evidence schema lost %q", key)
		}
	}
}

func TestHostedE2ECapabilityEvidenceWriterFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid key", body: `record_capability 'bad key' pass`},
		{name: "invalid state", body: `record_capability good maybe`},
		{name: "duplicate key", body: `record_capability good pass; record_capability good fail`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmp := t.TempDir()
			command := fmt.Sprintf(`
set -euo pipefail
export PODLAZ_E2E_CAPABILITY_SOURCE_ONLY=true
export E2E_TMP_ROOT=%q
export E2E_ARTIFACT_DIR=%q
mkdir -p "$E2E_TMP_ROOT" "$E2E_ARTIFACT_DIR"
source %q
%s
`, filepath.Join(tmp, "private"), filepath.Join(tmp, "public"), hostedCapabilityScript, test.body)
			cmd := exec.Command("bash", "-c", command)
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("invalid evidence was accepted; output=%s", output)
			}
		})
	}
}
