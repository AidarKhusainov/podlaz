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


func TestHostedVMBootRestartFailureUsesInstalledReadOnlyDiagnostics(t *testing.T) {
    script, err := os.ReadFile("hosted-vm-boot-authority.sh")
    if err != nil { t.Fatal(err) }
    body := string(script)
    for _, expected := range []string{
        "boot_restart_readonly_diagnostic()",
        "boot-restart-diagnostic.api=",
        "boot-restart-diagnostic.continuation=",
        "/var/tmp/podlaz-hosted-vm-daemon-status-semantics.py",
        "/var/tmp/podlaz-hosted-vm-tun/restart-status-diagnostic.json",
        "if ! hosted_vm_tun_wait_status verified-active 180; then",
    } {
        if !strings.Contains(body, expected) { t.Fatalf("missing diagnostic evidence %q", expected) }
    }
    for _, invalid := range []string{"/tmp/daemon_status_semantics.py", "/tmp/podlaz-hosted-vm-tun/"} {
        if strings.Contains(body, invalid) { t.Fatalf("diagnostic path does not match installed helper: %q", invalid) }
    }
    start := strings.Index(body, "boot_restart_readonly_diagnostic() {")
    end := strings.Index(body[start:], "\ncleanup() {")
    if start < 0 || end < 0 { t.Fatal("read-only diagnostic boundaries missing") }
    readonly := body[start:start+end]
    for _, forbidden := range []string{"journalctl", "systemctl restart", "recover --execute", "podlaz disconnect", "ip link del", "nft delete", "status.json"} {
        if strings.Contains(readonly, forbidden) { t.Fatalf("diagnostic must not expose private state or repair it: %q", forbidden) }
    }
}


func TestHostedRestartStatusPollingPreservesBoundAndReadOnlyClasses(t *testing.T) {
    script, err := os.ReadFile("hosted-vm-boot-authority.sh")
    if err != nil { t.Fatal(err) }
    if !strings.Contains(string(script), "hosted_vm_tun_wait_status verified-active 180 diagnose") {
        t.Fatal("same-boot restart must diagnose the unchanged 180-attempt predicate")
    }
    helper, err := os.ReadFile("lib/hosted_vm_tun.sh")
    if err != nil { t.Fatal(err) }
    body := string(helper)
    begin := strings.Index(body, "hosted_vm_tun_wait_status_diagnosed() {")
    end := strings.Index(body, "\nhosted_vm_tun_wait_status() {")
    if begin < 0 || end <= begin { t.Fatal("bounded poll helper missing") }
    poll := body[begin:end]
    for _, required := range []string{
        "attempt <= attempts", "sleep 1", "--max-time 5",
        "probe-verified-active", "guest-exec-failure",
        "transport-connect", "semantic-active-health",
        "boot-restart-poll.outcome=", "boot-restart-poll.attempts=",
    } {
        if !strings.Contains(poll, required) { t.Fatalf("poll diagnostics missing %q", required) }
    }
    for _, forbidden := range []string{"systemctl restart", "recover --execute", "journalctl", "ip link del", "nft delete", "profile-uri"} {
        if strings.Contains(poll, forbidden) { t.Fatalf("poll diagnostic must be read-only: %q", forbidden) }
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
