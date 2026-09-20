package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestHostedVMSuspendResumeOwnsActualPowerBoundary(t *testing.T) {
	script, err := os.ReadFile("hosted-vm-suspend-resume.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, marker := range []string{
		"hosted_vm_suspend_guest",
		"vm.suspend_wakeup_capability",
		"suspend.actual_guest_boundary",
		"suspend.same_boot",
		"privacy.direct_uplink_blocked_before_suspend",
		"privacy.direct_uplink_blocked_after_wakeup",
		"tun.same_network_session",
		"hosted_synthetic_active_authority.py",
		"hosted_synthetic_network_authority.py",
		"fixture.foreign_state_after_wakeup",
		"tun.exact_terminal_cleanup",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted VM suspend scenario is missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		"systemctl restart podlazd",
		"boot_continuation_prepare_simulated_later_boot",
		"systemd-nspawn",
		"ip link del podlaz0",
		"rm -f /run/podlaz/network-session-continuation.json",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("hosted VM suspend scenario contains forbidden repair/simulation marker %q", forbidden)
		}
	}
}

func TestHostedVMSuspendResumeUsesQMPHardwareSuspendControl(t *testing.T) {
	helper, err := os.ReadFile("lib/hosted_vm.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(helper)
	for _, marker := range []string{
		"-qmp \"unix:",
		"query-current-machine",
		"wakeup-suspend-support",
		"query-status",
		"system_wakeup",
		"guest-suspend-ram",
		"guest-exec",
		"qemu-guest-agent",
		"org.qemu.guest_agent.0",
		"HOSTED_VM_PROVIDER_TAP",
		"virtio-net-pci,netdev=pzprovider",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted VM helper is missing suspend control %q", marker)
		}
	}
}

func TestHostedVMSuspendResumeKeepsProviderOutsideGuest(t *testing.T) {
	script, err := os.ReadFile("hosted-vm-suspend-resume.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, marker := range []string{
		"hosted_vm_provider_prepare_host",
		"hosted_vm_provider_prepare_guest",
		"\"listen\": \"${VM_ENDPOINT_IP}\"",
		"VM_ENDPOINT_IP=\"${HOSTED_VM_PROVIDER_HOST_IP}\"",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted VM suspend provider topology is missing %q", marker)
		}
	}
}

func TestHostedVMSuspendResumeWorkflowIsFocused(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/hosted-vm-suspend-resume.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(workflow))
	for _, marker := range []string{
		"ubuntu-24.04",
		"qemu-system-x86",
		"hosted-vm-suspend-resume.sh",
		"real guest suspend and wake",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted VM suspend workflow is missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		"autostart",
		"wifi",
		"package upgrade",
		"real-provider",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("hosted VM suspend workflow mixes another lifecycle domain: %q", forbidden)
		}
	}
}
