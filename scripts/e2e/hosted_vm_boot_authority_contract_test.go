package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestHostedVMBootAuthorityCoversSuccessfulAttemptAndDisconnectFence(t *testing.T) {
	script, err := os.ReadFile("hosted-vm-boot-authority.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, marker := range []string{
		"hosted_vm_reboot",
		"podlaz.boot-autostart-manifest.v1",
		"podlaz.boot-autostart-attempt.v1",
		"autostart.succeeded_once",
		"autostart.attempt_generation_exact",
		"daemon_restart.attempt_unchanged",
		"daemon_restart.session_unchanged",
		"daemon_restart_failure_token",
		"diagnose-active",
		"network-session-resume.json",
		"explicit_disconnect.attempt_unchanged",
		"same_boot_restart.clean_inactive",
		"same_boot_restart.no_session",
		"privacy.direct_uplink_blocked_after_boot",
		"explicit_disconnect.exact_terminal_cleanup",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("boot authority scenario missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		"boot_continuation_prepare_simulated_later_boot",
		"rm -f /run/podlaz/boot-autostart-attempt.json",
		"rm -f /run/podlaz/network-session-continuation.json",
		"ip link del podlaz0",
		"systemd-nspawn",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("boot authority scenario contains forbidden repair marker %q", forbidden)
		}
	}
}

func TestHostedVMBootAuthorityFailureDiagnosticsAreReadOnlyAndBounded(t *testing.T) {
	data, err := os.ReadFile("hosted-vm-boot-authority.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start := strings.Index(text, "boot_restart_readonly_diagnostic() {")
	end := strings.Index(text[start:], "\ncleanup() {")
	if start < 0 || end < 0 {
		t.Fatal("bounded boot restart diagnostics must precede cleanup")
	}
	body := text[start : start+end]
	for _, required := range []string{
		"boot-restart-diagnostic.service=",
		"boot-restart-diagnostic.socket=",
		"boot-restart-diagnostic.continuation=",
		"boot-restart-diagnostic.api=",
		"boot-restart-diagnostic.dns_scope_cause=",
		"diagnose-dns-scope-cause",
		"--max-time 5",
		"--output /dev/null",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("read-only diagnostics lost %q", required)
		}
	}
	for _, forbidden := range []string{
		"journalctl", "cat /run/podlaz/network-session-continuation.json",
		"systemctl restart", "systemctl start", "recover --execute",
		"podlaz connect", "podlaz disconnect", "ip link del", "nft delete",
		"status.json", "profile-uri",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("read-only diagnostic contains forbidden %q", forbidden)
		}
	}
	if !strings.Contains(text, "if ! hosted_vm_tun_wait_status verified-active 180 diagnose; then\n    boot_restart_readonly_diagnostic") {
		t.Fatal("boot restart must retain the 180-attempt verified-active bound")
	}
}

func TestHostedVMBootAuthorityUsesSharedSyntheticTUNMechanics(t *testing.T) {
	helper, err := os.ReadFile("lib/hosted_vm_tun.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(helper)
	for _, marker := range []string{
		"hosted_vm_provider_prepare_host",
		"hosted_vm_provider_prepare_guest",
		"hosted_synthetic_active_authority.py",
		"hosted_synthetic_network_authority.py",
		"verify-present",
		"verify-absent",
		"configure-autostart",
		"curl -4 -fsSk --interface",
		"podlaz-vm-foreign.service",
		"--unix-socket /run/podlaz/podlazd.sock",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("shared VM TUN helper missing %q", marker)
		}
	}
}

func TestHostedVMBootAuthorityWorkflowIsFocused(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/hosted-vm-boot-authority.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(workflow))
	for _, marker := range []string{
		"ubuntu-24.04",
		"qemu-system-x86",
		"hosted-vm-boot-authority.sh",
		"real boot autostart and same-boot authority",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("boot authority workflow missing %q", marker)
		}
	}
	for _, forbidden := range []string{"suspend", "wifi", "package upgrade", "real-provider", "terminal boot"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("boot authority workflow mixes another domain %q", forbidden)
		}
	}
}
