package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readScenario(t *testing.T, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(contents)
}

func requireScenarioFragments(t *testing.T, name string, fragments ...string) {
	t.Helper()
	text := readScenario(t, name)
	for _, fragment := range fragments {
		if !strings.Contains(text, fragment) {
			t.Fatalf("%s missing %q", name, fragment)
		}
	}
}

func forbidScenarioFragments(t *testing.T, name string, fragments ...string) {
	t.Helper()
	text := readScenario(t, name)
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			t.Fatalf("%s unexpectedly contains %q", name, fragment)
		}
	}
}

func TestReadinessCallersPreserveScenarioTimeoutContracts(t *testing.T) {
	requireScenarioFragments(t, "package-failure-package-acceptance.sh", `wait_for_daemon_socket "${DAEMON_SOCKET}" 15`)
	requireScenarioFragments(t, "tun-package-convergence.sh", `wait_for_daemon_socket "${DAEMON_SOCKET}" 15`)
	requireScenarioFragments(t, "tun-resource-soak.sh", `wait_for_daemon_socket "${DAEMON_SOCKET}" 15`)
	requireScenarioFragments(t, "package-lifecycle-acceptance.sh", `wait_for_daemon_ready "${DAEMON_SOCKET}" podlazd.service 15`)
	requireScenarioFragments(t, "network-resource-coexistence-package-acceptance.sh", `wait_for_daemon_ready "${DAEMON_SOCKET}" podlazd.service 15`)
	requireScenarioFragments(t, "network-recovery-package-acceptance.sh",
		`wait_for_daemon_ready "${DAEMON_SOCKET}" podlazd.service 20`,
		`wait_for_semantic_status "${phase}" 60 active`,
		`wait_for_semantic_status "${phase}" 30 inactive`,
	)
	requireScenarioFragments(t, "privacy-envelope-lifecycle-package-acceptance.sh",
		`wait_for_service_active podlazd.service 30`,
		`wait_for_status_match "${phase}: protected TUN active state" 60 status_matches active active`,
		`wait_for_status_match "${phase}: clean inactive state" 40 status_matches inactive disabled`,
	)
	requireScenarioFragments(t, "network-reconciliation-package-acceptance.sh",
		`wait_for_service_active podlazd.service 40`,
		`wait_for_status_match "${phase}" 120 status_is_verified_active`,
		`wait_for_status_match "${phase}" 80 status_is_inactive`,
	)
	requireScenarioFragments(t, filepath.Join("lib", "boot_continuation.sh"),
		`wait_for_daemon_ready "${BOOT_CONTINUATION_DAEMON_SOCKET}" podlazd.service 30`,
		`wait_for_status_match "boot-continuation TUN session" 120`,
		`wait_for_status_match "boot-continuation daemon inactive state" 80`,
	)
}

func TestExactExecutionAndInputDuplicatesUseFocusedLibraries(t *testing.T) {
	for _, name := range []string{
		"data-plane.sh", "tun-fault-injection.sh", "tun-package-convergence.sh", "tun-resource-soak.sh",
		"package-failure-package-acceptance.sh", "package-lifecycle-acceptance.sh", "protected-gateway-package-acceptance.sh",
		"network-recovery-package-acceptance.sh", "privacy-envelope-lifecycle-package-acceptance.sh",
		"network-reconciliation-package-acceptance.sh", "network-resource-coexistence-package-acceptance.sh",
	} {
		forbidScenarioFragments(t, name, "\nrun_installed_podlaz() {", "\nfirst_profile_uri() {")
	}

	for _, name := range []string{"stale-link-package-acceptance.sh", "log-window-acceptance.sh"} {
		requireScenarioFragments(t, name, `source "${SCRIPT_DIR}/lib/bounded_client.sh"`)
		forbidScenarioFragments(t, name, "\nrun_installed_podlaz_bounded() {")
	}

	for _, name := range []string{"data-plane.sh", "tun-fault-injection.sh", "tun-package-convergence.sh", "tun-resource-soak.sh"} {
		requireScenarioFragments(t, name, `source "${SCRIPT_DIR}/lib/profile_input.sh"`, "first_configured_profile_uri")
	}
	requireScenarioFragments(t, "tun-package-convergence.sh",
		`source "${SCRIPT_DIR}/lib/installed_client.sh"`,
		`source "${SCRIPT_DIR}/lib/host_observation.sh"`,
		`append_sensitive_value "$(observe_host_sensitive_values)"`,
	)
	requireScenarioFragments(t, "tun-resource-soak.sh", `source "${SCRIPT_DIR}/lib/installed_client.sh"`)
	requireScenarioFragments(t, filepath.Join("lib", "boot_continuation.sh"),
		`source "${SCRIPT_DIR}/lib/profile_input.sh"`,
		"boot_continuation_first_profile_uri() {\n  first_configured_profile_uri\n}",
	)
}

func TestScenarioSpecificAuthorityRemainsLocal(t *testing.T) {
	requireScenarioFragments(t, "package-failure-package-acceptance.sh",
		"verify_package_provenance() {",
		`running_exe="$(sudo -n readlink -f "/proc/${main_pid}/exe")"`,
		`running_hash="$(sudo -n sha256sum "/proc/${main_pid}/exe" | awk '{print $1}')"`,
	)
	requireScenarioFragments(t, "remote-client-acceptance.sh", "run_ordinary_podlaz() {", "run_privileged_podlaz() {", "Deliberately local.")
	forbidScenarioFragments(t, "remote-client-acceptance.sh", `source "${SCRIPT_DIR}/lib/installed_client.sh"`)
	for _, name := range []string{"data-plane.sh", "tun-fault-injection.sh"} {
		requireScenarioFragments(t, name, "run_podlaz_as_socket_user() {", "wait_for_daemon_socket() {", "Deliberately local:")
	}
	requireScenarioFragments(t, "tun-resource-soak.sh",
		"collect_host_sensitive_values() {",
		"nmcli --terse --escape no --fields UUID connection show --active",
		"resolvectl status --no-pager",
		"run_installed_podlaz_bounded() {",
		`"${timeout_seconds}s"`,
		"Deliberately local:",
	)
	for _, tc := range []struct {
		name      string
		predicate string
	}{
		{"network-recovery-package-acceptance.sh", "daemon_status_classify() {"},
		{"privacy-envelope-lifecycle-package-acceptance.sh", "status_matches() {"},
		{"network-reconciliation-package-acceptance.sh", "status_is_verified_active() {"},
	} {
		requireScenarioFragments(t, tc.name, tc.predicate)
	}
}

func TestRenamedScenarioArtifactsDoNotRegressToIssueNumberLabels(t *testing.T) {
	legacy := regexp.MustCompile(`(?i)issue[ _-]?[0-9]+`)
	for _, name := range []string{
		"data-plane.sh", "tun-fault-injection.sh", "tun-package-convergence.sh", "tun-resource-soak.sh",
		"package-failure-package-acceptance.sh", "protected-gateway-package-acceptance.sh", "stale-link-package-acceptance.sh",
		"log-window-acceptance.sh", "remote-client-acceptance.sh", "package-lifecycle-acceptance.sh",
		"network-recovery-package-acceptance.sh", "network-resource-coexistence-package-acceptance.sh",
		"privacy-envelope-lifecycle-package-acceptance.sh", "network-reconciliation-package-acceptance.sh",
		filepath.Join("lib", "boot_continuation.sh"),
	} {
		text := readScenario(t, name)
		if match := legacy.FindString(text); match != "" {
			t.Fatalf("%s still contains legacy issue-number label %q", name, match)
		}
	}
}
