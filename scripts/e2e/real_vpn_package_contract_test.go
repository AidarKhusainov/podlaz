package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestRealVPNSupportsExactPrebuiltPackageTunContract(t *testing.T) {
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
		`source "${SCRIPT_DIR}/lib/recovery_json.sh"`,
		`source "${SCRIPT_DIR}/lib/private_command.sh"`,
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
		`assert_clean_recovery_json_file`,
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("real VPN E2E missing exact-package TUN contract %q", fragment)
		}
	}
	for _, privateCommand := range []string{
		"real-profile-show",
		"real-profile-show-json",
		"real-profile-validate-proxy-only",
		"real-profile-validate-tun",
		"real-plan-proxy-only",
		"real-plan-tun-dry-run",
	} {
		if !strings.Contains(text, `expect_private_success `+privateCommand) {
			t.Fatalf("real VPN profile-derived output must stay private for %q", privateCommand)
		}
		if strings.Contains(text, `expect_success `+privateCommand) {
			t.Fatalf("real VPN must not log profile-derived output for %q", privateCommand)
		}
	}
	if strings.Contains(text, `run_podlaz_as_socket_user status --json`) {
		t.Fatal("exact-package TUN smoke must not depend on deferred public CLI status --json")
	}
	if strings.Contains(text, `status.get("tun")`) {
		t.Fatal("exact-package TUN smoke must not classify lifecycle authority from presentation-only TUN status")
	}
}
