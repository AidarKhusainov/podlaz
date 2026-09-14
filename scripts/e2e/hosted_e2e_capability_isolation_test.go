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
	qemuMarker := filepath.Join(tmp, "qemu-probe-ran")

	command := `
set -euo pipefail
export PODLAZ_E2E_CAPABILITY_SOURCE_ONLY=true
export E2E_TMP_ROOT="$1/private"
export E2E_ARTIFACT_DIR="$1/public"
mkdir -p "$E2E_TMP_ROOT" "$E2E_ARTIFACT_DIR"
source ./hosted-e2e-capability.sh
run_system_guest_capability() { : >"$2"; return 1; }
run_qemu_capability() { : >"$3"; return 0; }
set +e
run_independent_capability_probes
code=$?
set -e
[[ "$code" -ne 0 ]]
[[ -f "$2" ]]
[[ -f "$3" ]]
`
	cmd := exec.Command("bash", "-c", command, "bash", tmp, systemMarker, qemuMarker)
	cmd.Dir = "."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("independent capability orchestration failed: %v\n%s", err, output)
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
