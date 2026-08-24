package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestIssue263PackageAcceptanceCoversBootAutostartLifecycle(t *testing.T) {
	data, err := os.ReadFile("issue263-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read issue263 acceptance: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"autostart_disabled_same_boot",
		"autostart_enabled_next_boot",
		"daemon_restart_preserved_session",
		"package_upgrade_preserved_session",
		"explicit_disconnect_no_restart_reconnect",
		"terminal_autostart_failure",
		"terminal_no_same_boot_retry",
		"autostart disable",
		"autostart enable --mode tun",
		"systemctl restart podlazd.service",
		"dpkg -i",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("issue 263 acceptance must contain %q", required)
		}
	}
}

func TestIssue263PackageAcceptanceAvoidsManualNetworkRepair(t *testing.T) {
	data, err := os.ReadFile("issue263-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read issue263 acceptance: %v", err)
	}
	lower := strings.ToLower(string(data))
	for _, forbidden := range []string{
		"podlaz recover --execute",
		"ip rule del",
		"ip route del",
		"resolvectl revert",
		"nft delete table",
		"systemctl restart networkmanager",
		"systemctl restart systemd-resolved",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("issue 263 acceptance success path must not contain manual repair %q", forbidden)
		}
	}
}

func TestIssue263WorkflowRunsInstalledPackageAcceptance(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/e2e-tun-package-convergence.yml")
	if err != nil {
		t.Fatalf("read package convergence workflow: %v", err)
	}
	workflow := string(data)
	for _, required := range []string{
		"Run issue 263 boot autostart acceptance",
		"bash scripts/e2e/issue263-package-acceptance.sh",
		"timeout-minutes: 90",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("issue 263 workflow wiring must contain %q", required)
		}
	}
}
