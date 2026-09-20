package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestHostedVMTerminalBootPreservesOneTerminalAttempt(t *testing.T) {
	script, err := os.ReadFile("hosted-vm-terminal-boot.sh")
	if err != nil { t.Fatal(err) }
	text := string(script)
	for _, marker := range []string{
		"vpn.invalid",
		"podlaz.boot-autostart-attempt.v1",
		"terminal_reason==\"connect_failed\"",
		"terminal_attempt.generation_exact",
		"same_boot_restart.attempt_unchanged",
		"same_boot_restart.no_session",
		"same_boot_restart.no_retry",
		"Reason: VPN connection could not be established safely",
		"terminal_cleanup.exact",
	} {
		if !strings.Contains(text, marker) { t.Fatalf("terminal boot scenario missing %q", marker) }
	}
	for _, forbidden := range []string{
		"boot_continuation_prepare_simulated_later_boot",
		"rm -f /run/podlaz/boot-autostart-attempt.json",
		"rm -f /run/podlaz/network-session-continuation.json",
		"ip link del podlaz0",
		"systemd-nspawn",
	} {
		if strings.Contains(text, forbidden) { t.Fatalf("terminal boot scenario contains forbidden repair %q", forbidden) }
	}
}

func TestHostedVMTerminalBootWorkflowIsFocused(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/hosted-vm-terminal-boot.yml")
	if err != nil { t.Fatal(err) }
	text := strings.ToLower(string(workflow))
	for _, marker := range []string{"ubuntu-24.04","qemu-system-x86","hosted-vm-terminal-boot.sh","terminal boot attempt stays consumed"} {
		if !strings.Contains(text, marker) { t.Fatalf("terminal boot workflow missing %q", marker) }
	}
	for _, forbidden := range []string{"suspend","wifi","package upgrade","real-provider"} {
		if strings.Contains(text, forbidden) { t.Fatalf("terminal boot workflow mixes another domain %q", forbidden) }
	}
}
