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
		"autostart enable \"${BOOT_CONTINUATION_PROFILE_ID}\"",
		"boot_continuation_restart_daemon",
		"dpkg -i",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("boot-continuation acceptance must contain %q", required)
		}
	}
}

func TestBootContinuationCurrentCandidateUsesCanonicalCLI(t *testing.T) {
	helperData, err := os.ReadFile("lib/boot_continuation.sh")
	if err != nil {
		t.Fatal(err)
	}
	acceptanceData, err := os.ReadFile("boot-continuation-package-acceptance.sh")
	if err != nil {
		t.Fatal(err)
	}
	helper := string(helperData)
	acceptance := string(acceptanceData)

	for _, want := range []string{
		"boot_continuation_run_podlaz import",
		"profiles.json",
		"selected_profile_id",
	} {
		if !strings.Contains(helper, want) {
			t.Fatalf("boot-continuation helper missing canonical import state %q", want)
		}
	}
	for _, forbidden := range []string{
		"podlaz profile import",
		"boot_continuation_run_podlaz profile import",
		"Imported profile:",
	} {
		if strings.Contains(helper, forbidden) {
			t.Fatalf("boot-continuation helper retained old CLI contract %q", forbidden)
		}
	}
	if strings.Contains(acceptance, "--mode tun") {
		t.Fatalf("boot-continuation acceptance retained public mode matrix")
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
