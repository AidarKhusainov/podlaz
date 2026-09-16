package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestHostedE2ECapabilityIndependentProbesContinueAfterSystemGuestFailure(t *testing.T) {
	tmp := t.TempDir()
	systemMarker := filepath.Join(tmp, "system-probe-ran")
	systemAfterFailureMarker := filepath.Join(tmp, "system-probe-continued-after-failure")
	qemuMarker := filepath.Join(tmp, "qemu-probe-ran")

	command := `
set -euo pipefail
export PODLAZ_E2E_CAPABILITY_SOURCE_ONLY=true
export E2E_TMP_ROOT="$1/private"
export E2E_ARTIFACT_DIR="$1/public"
SYSTEM_MARKER="$2"
SYSTEM_AFTER_FAILURE_MARKER="$3"
QEMU_MARKER="$4"
mkdir -p "$E2E_TMP_ROOT" "$E2E_ARTIFACT_DIR"
source ./hosted-e2e-capability.sh
run_system_guest_capability() (
  set -e
  : >"${SYSTEM_MARKER}"
  false
  : >"${SYSTEM_AFTER_FAILURE_MARKER}"
)
run_qemu_capability() { : >"${QEMU_MARKER}"; return 0; }
set +e
run_independent_capability_probes
code=$?
set -e
[[ "$code" -ne 0 ]]
[[ -f "${SYSTEM_MARKER}" ]]
[[ ! -e "${SYSTEM_AFTER_FAILURE_MARKER}" ]]
[[ -f "${QEMU_MARKER}" ]]
`
	cmd := exec.Command("bash", "-c", command, "bash", tmp, systemMarker, systemAfterFailureMarker, qemuMarker)
	cmd.Dir = "."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("independent capability orchestration failed: %v\n%s", err, output)
	}
}

func TestHostedE2ECapabilityMainDoesNotDisableProbeErrexit(t *testing.T) {
	data, err := os.ReadFile(hostedCapabilityScript)
	if err != nil {
		t.Fatalf("read hosted capability script: %v", err)
	}
	if strings.Contains(string(data), "if ! run_independent_capability_probes; then") {
		t.Fatal("main must collect probe status without invoking the orchestrator in conditional context")
	}
}

func TestHostedE2ECapabilityFindLoopbackPort(t *testing.T) {
	tmp := t.TempDir()
	command := `
set -euo pipefail
export PODLAZ_E2E_CAPABILITY_SOURCE_ONLY=true
export E2E_TMP_ROOT="$1/private"
export E2E_ARTIFACT_DIR="$1/public"
mkdir -p "$E2E_TMP_ROOT" "$E2E_ARTIFACT_DIR"
source ./hosted-e2e-capability.sh
find_loopback_port
`
	cmd := exec.Command("bash", "-c", command, "bash", tmp)
	cmd.Dir = "."
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("find_loopback_port failed: %v\n%s", err, output)
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatalf("find_loopback_port returned non-numeric output %q: %v", output, err)
	}
	if port < 1 || port > 65535 {
		t.Fatalf("find_loopback_port returned invalid port %d", port)
	}
}

func TestHostedE2ECapabilityBootstrapsMinbaseBeforeGuestPackages(t *testing.T) {
	data, err := os.ReadFile(hostedCapabilityScript)
	if err != nil {
		t.Fatalf("read hosted capability script: %v", err)
	}
	script := string(data)
	debootstrap := strings.Index(script, "--variant=minbase")
	guestPackages := strings.Index(script, "apt-get install -y --no-install-recommends")
	if debootstrap < 0 {
		t.Fatal("hosted capability must bootstrap a Noble minbase rootfs")
	}
	if guestPackages < 0 {
		t.Fatal("hosted capability must install the guest runtime with apt after minbase bootstrap")
	}
	if guestPackages < debootstrap {
		t.Fatal("guest runtime package installation must happen after minbase debootstrap")
	}
	if strings.Contains(script[debootstrap:guestPackages], "--include=") {
		t.Fatal("debootstrap must not install the desktop-like guest runtime through --include")
	}
}

func TestHostedE2ECapabilityBoundsSystemGuestReadiness(t *testing.T) {
	data, err := os.ReadFile(hostedCapabilityScript)
	if err != nil {
		t.Fatalf("read hosted capability script: %v", err)
	}
	if !strings.Contains(string(data), "timeout 30 systemctl is-system-running --wait") {
		t.Fatal("system guest readiness wait must have an explicit bounded timeout")
	}
}

func TestHostedE2ECapabilityChecksGuestUplinkIdentityBeforeNetworkManager(t *testing.T) {
	data, err := os.ReadFile(hostedCapabilityScript)
	if err != nil {
		t.Fatalf("read hosted capability script: %v", err)
	}
	script := string(data)
	interfaceCheck := strings.Index(script, "guest.start.interface")
	nmActivate := strings.Index(script, "nmcli connection up capability-uplink")
	if interfaceCheck < 0 {
		t.Fatal("system guest startup must classify the guest uplink interface before NetworkManager activation")
	}
	if nmActivate < 0 {
		t.Fatal("system guest startup must activate the NetworkManager capability uplink")
	}
	if interfaceCheck > nmActivate {
		t.Fatal("guest uplink identity must be checked before NetworkManager activation")
	}
	if !strings.Contains(script, "ip -o link show dev \"${CAPABILITY_GUEST_IF}\"") {
		t.Fatal("system guest startup must verify the expected guest veth identity")
	}
}

func TestHostedE2ECapabilityScopesNetworkManagerManagedOverride(t *testing.T) {
	data, err := os.ReadFile(hostedCapabilityScript)
	if err != nil {
		t.Fatalf("read hosted capability script: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"20-capability-uplink.conf",
		"[device-capability-uplink]",
		"match-device=interface-name:=${CAPABILITY_GUEST_IF}",
		"managed=1",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("hosted capability must scope the NetworkManager managed override with %q", required)
		}
	}
	for _, forbidden := range []string{
		"match-device=*",
		"match-device=interface-name:*",
		"unmanaged-devices=*",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("hosted capability must not use broad NetworkManager override %q", forbidden)
		}
	}
}

func TestHostedE2ECapabilityReportsGuestBootstrapStages(t *testing.T) {
	data, err := os.ReadFile(hostedCapabilityScript)
	if err != nil {
		t.Fatalf("read hosted capability script: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"guest.bootstrap.prepare",
		"guest.bootstrap.start",
		"guest.prepare.debootstrap",
		"guest.prepare.networking",
		"guest.prepare.user",
		"guest.prepare.services",
		"guest.prepare.candidate",
		"guest.start.nspawn",
		"guest.start.control",
		"guest.start.interface",
		"guest.start.uplink",
		"guest.start.services",
		"guest.start.tun",
		"guest.start.internet",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("hosted capability evidence must expose %q", required)
		}
	}
}
