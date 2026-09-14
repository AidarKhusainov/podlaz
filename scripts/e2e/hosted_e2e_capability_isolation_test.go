package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestHostedE2ECapabilityReportsGuestBootstrapStages(t *testing.T) {
	data, err := os.ReadFile(hostedCapabilityScript)
	if err != nil {
		t.Fatalf("read hosted capability script: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"guest.bootstrap.prepare",
		"guest.bootstrap.start",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("hosted capability evidence must expose %q", required)
		}
	}
}
