package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestHostedVMBootAuthorityCoversSuccessfulAttemptAndDisconnectFence(t *testing.T) {
	script, err := os.ReadFile("hosted-vm-boot-authority.sh")
	if err != nil { t.Fatal(err) }
	text := string(script)
	for _, marker := range []string{
		"hosted_vm_reboot",
		"podlaz.boot-autostart-manifest.v1",
		"podlaz.boot-autostart-attempt.v1",
		"autostart.succeeded_once",
		"autostart.attempt_generation_exact",
		"daemon_restart.attempt_unchanged",
		"daemon_restart.session_unchanged",
		"explicit_disconnect.attempt_unchanged",
		"same_boot_restart.clean_inactive",
		"same_boot_restart.no_session",
		"privacy.direct_uplink_blocked_after_boot",
		"explicit_disconnect.exact_terminal_cleanup",
	} {
		if !strings.Contains(text, marker) { t.Fatalf("boot authority scenario missing %q", marker) }
	}
	for _, forbidden := range []string{
		"boot_continuation_prepare_simulated_later_boot",
		"rm -f /run/podlaz/boot-autostart-attempt.json",
		"rm -f /run/podlaz/network-session-continuation.json",
		"ip link del podlaz0",
		"systemd-nspawn",
	} {
		if strings.Contains(text, forbidden) { t.Fatalf("boot authority scenario contains forbidden repair marker %q", forbidden) }
	}
}

func TestHostedVMBootAuthorityUsesSharedSyntheticTUNMechanics(t *testing.T) {
	helper, err := os.ReadFile("lib/hosted_vm_tun.sh")
	if err != nil { t.Fatal(err) }
	text := string(helper)
	for _, marker := range []string{
		"guestfwd=tcp:",
		"hosted_synthetic_active_authority.py",
		"hosted_synthetic_network_authority.py",
		"verify-present",
		"verify-absent",
		"configure-autostart",
		"curl -4 -fsSk --interface",
	} {
		if !strings.Contains(text, marker) { t.Fatalf("shared VM TUN helper missing %q", marker) }
	}
}

func TestHostedVMBootAuthorityWorkflowIsFocused(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/hosted-vm-boot-authority.yml")
	if err != nil { t.Fatal(err) }
	text := strings.ToLower(string(workflow))
	for _, marker := range []string{"ubuntu-24.04","qemu-system-x86","hosted-vm-boot-authority.sh","real boot autostart and same-boot authority"} {
		if !strings.Contains(text, marker) { t.Fatalf("boot authority workflow missing %q", marker) }
	}
	for _, forbidden := range []string{"suspend","wifi","package upgrade","real-provider","terminal boot"} {
		if strings.Contains(text, forbidden) { t.Fatalf("boot authority workflow mixes another domain %q", forbidden) }
	}
}
