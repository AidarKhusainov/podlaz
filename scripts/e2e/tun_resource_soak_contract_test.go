package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestTunResourceSoakPreservesLifecycleAndPrivacyBoundaries(t *testing.T) {
	data, err := os.ReadFile("tun-resource-soak.sh")
	if err != nil {
		t.Fatalf("read TUN resource soak harness: %v", err)
	}
	text := string(data)

	ordered := []string{
		"precondition_warmed_inactive_baseline",
		`SOAK_PHASE="session-one-connect"`,
		`--output "${SESSION_ONE_IDENTITY}"`,
		`sleep "${PODLAZ_E2E_SOAK_WARMUP_SECONDS}"`,
		"run_active_soak",
		"disconnect_and_sample_cleanup",
		"run_reconnect_probe",
		"write_public_report",
		`rm -rf -- "${SOAK_PRIVATE_DIR}"`,
		"assert_artifacts_do_not_contain_sensitive_values",
	}
	previous := -1
	for _, marker := range ordered {
		index := strings.LastIndex(text, marker)
		if index < 0 {
			t.Fatalf("TUN resource soak lost lifecycle boundary %q", marker)
		}
		if index <= previous {
			t.Fatalf("TUN resource soak lifecycle boundary %q is out of order", marker)
		}
		previous = index
	}

	for _, required := range []string{
		`assert-replaced`,
		`assert-gone`,
		`NETWORK_ISOLATION_BASELINE`,
		`PACKAGE_BUILD_LOG="${SOAK_PRIVATE_DIR}/package-build.log"`,
		`PACKAGE_INSTALL_LOG="${SOAK_PRIVATE_DIR}/package-install.log"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("TUN resource soak lost %q", required)
		}
	}
}
