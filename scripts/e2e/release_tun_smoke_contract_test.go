package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseRequiresExactPackageTunSmokeBeforePublish(t *testing.T) {
	workflowPath := filepath.Join("..", "..", ".github", "workflows", "release.yml")
	contents, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(contents)
	for _, fragment := range []string{
		"tun-smoke:",
		"name: Exact-package TUN smoke",
		"runs-on: [self-hosted, linux, x64, vpn-e2e, ubuntu-24.04]",
		"environment: vpn-e2e",
		"PODLAZ_E2E_PROFILE_URI: ${{ secrets.PODLAZ_E2E_PROFILE_URI }}",
		"PODLAZ_E2E_EXPECTED_EGRESS_IP: ${{ secrets.PODLAZ_E2E_EXPECTED_EGRESS_IP }}",
		"uses: actions/download-artifact@v8",
		"name: podlaz-release-artifacts-${{ needs.resolve.outputs.version }}",
		"PODLAZ_E2E_DEB_PATH:",
		"PODLAZ_E2E_EXPECT_COMMIT: ${{ needs.build.outputs.commit_sha }}",
		"PODLAZ_E2E_ENABLE_LIFECYCLE: 'true'",
		"PODLAZ_E2E_ENABLE_TUN: 'true'",
		`test -n "${PODLAZ_E2E_EXPECTED_EGRESS_IP}"`,
		"bash scripts/e2e/real-vpn.sh",
	} {
		if !strings.Contains(workflow, fragment) {
			t.Fatalf("release workflow missing exact-package TUN smoke contract %q", fragment)
		}
	}
	publishDependsOnSmoke := regexp.MustCompile(`(?s)attest-and-publish:.*?needs:\s*\n(?:\s*- .*\n)*?\s*- tun-smoke(?:\s|$)`)
	if !publishDependsOnSmoke.MatchString(workflow) {
		t.Fatal("release publication must depend on successful exact-package TUN smoke")
	}
}

func TestRealVPNSupportsPrebuiltPackageProvenance(t *testing.T) {
	contents, err := os.ReadFile("real-vpn.sh")
	if err != nil {
		t.Fatalf("read real VPN E2E: %v", err)
	}
	text := string(contents)
	for _, fragment := range []string{
		`PODLAZ_E2E_DEB_PATH`,
		`PODLAZ_E2E_EXPECT_COMMIT`,
		`source "${SCRIPT_DIR}/lib/package_provenance.sh"`,
		`source "${SCRIPT_DIR}/lib/status_polling.sh"`,
		`if [[ -n "${PODLAZ_E2E_DEB_PATH}" ]]; then`,
		`DEV_DEB="${PODLAZ_E2E_DEB_PATH}"`,
		`assert_native_deb_arch "${DEV_DEB}" "$(dpkg --print-architecture)"`,
		`assert_installed_package_version_matches_deb "${DEV_DEB}" podlaz`,
		`assert_installed_podlaz_commit "${PODLAZ_E2E_EXPECT_COMMIT}"`,
		`assert_running_podlazd_matches_deb "${DEV_DEB}"`,
		`http://localhost/v1/status`,
		`wait_for_status_match "real TUN verified active"`,
		`python3 "${SCRIPT_DIR}/lib/daemon_status_semantics.py" verified-active`,
		`wait_for_status_match "real TUN clean inactive"`,
		`python3 "${SCRIPT_DIR}/lib/daemon_status_semantics.py" clean-inactive`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("real VPN E2E missing prebuilt-package contract %q", fragment)
		}
	}
	if strings.Contains(text, `run_podlaz_as_socket_user status --json`) {
		t.Fatal("release smoke must not depend on deferred public CLI status --json")
	}
	if strings.Contains(text, `status.get("tun")`) {
		t.Fatal("release smoke must not classify lifecycle authority from presentation-only TUN status")
	}
}
