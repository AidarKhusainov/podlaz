package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestHostedVMRebootAutostartDisabledOwnsRealBootBoundary(t *testing.T) {
	script, err := os.ReadFile("hosted-vm-reboot-autostart-disabled.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, marker := range []string{
		"hosted_vm_reboot",
		"/proc/sys/kernel/random/boot_id",
		"autostart disable",
		"/run/podlaz/boot-autostart-attempt.json",
		"/var/lib/podlaz/boot-autostart-manifest.json",
		"/run/podlaz/network-session-continuation.json",
		"podlaz.terminal_authority_clean",
		"guest.ordinary_connectivity",
		"capability.kvm",
		"failure.class",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted VM reboot scenario is missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		"boot_continuation_prepare_simulated_later_boot",
		"systemd-nspawn",
		"ip link del podlaz0",
		"rm -f /run/podlaz/network-session-continuation.json",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("hosted VM reboot scenario contains forbidden repair/simulation marker %q", forbidden)
		}
	}
}

func TestHostedVMRebootAutostartDisabledUsesBoundedQEMUInfrastructure(t *testing.T) {
	helper, err := os.ReadFile("lib/hosted_vm.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(helper)
	for _, marker := range []string{
		"cloud-images.ubuntu.com/releases/noble/release",
		"SHA256SUMS",
		"-accel tcg",
		"127.0.0.1:",
		"hosted_vm_reboot()",
		"q35,accel=",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted VM helper is missing %q", marker)
		}
	}
	if strings.Contains(text, "-enable-kvm") {
		t.Fatal("hosted VM helper must not require KVM")
	}
}

func TestHostedVMRebootAutostartDisabledWorkflowIsFocused(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/hosted-vm-reboot-autostart-disabled.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflow)
	for _, marker := range []string{
		"ubuntu-24.04",
		"qemu-system-x86",
		"cloud-image-utils",
		"hosted-vm-reboot-autostart-disabled.sh",
		"Real reboot with autostart disabled",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted VM reboot workflow is missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		"rtcwake",
		"wifi",
		"package upgrade",
		"real-provider",
	} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("hosted VM reboot workflow mixes another lifecycle domain: %q", forbidden)
		}
	}
}
