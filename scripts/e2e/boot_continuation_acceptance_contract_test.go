package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestBootContinuationPackageAcceptanceCoversBootAutostartLifecycle(t *testing.T) {
	data, err := os.ReadFile("boot-continuation-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read boot-continuation acceptance: %v", err)
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
		"boot_continuation_restart_daemon",
		"dpkg -i",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("boot-continuation acceptance must contain %q", required)
		}
	}
}

func TestBootContinuationRestartHelperUsesPackagedSystemdRestart(t *testing.T) {
	data, err := os.ReadFile("lib/boot_continuation.sh")
	if err != nil {
		t.Fatalf("read boot-continuation helper: %v", err)
	}
	helper := string(data)
	for _, required := range []string{
		"boot_continuation_restart_daemon()",
		"systemctl restart podlazd.service",
		"boot_continuation_wait_for_daemon",
	} {
		if !strings.Contains(helper, required) {
			t.Fatalf("boot-continuation restart helper must contain %q", required)
		}
	}
}

func TestBootContinuationPackageAcceptanceAvoidsManualNetworkRepair(t *testing.T) {
	data, err := os.ReadFile("boot-continuation-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read boot-continuation acceptance: %v", err)
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
			t.Fatalf("boot-continuation acceptance success path must not contain manual repair %q", forbidden)
		}
	}
}
