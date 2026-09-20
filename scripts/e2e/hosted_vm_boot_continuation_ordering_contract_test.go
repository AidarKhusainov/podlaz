package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestHostedVMBootContinuationOrderingUsesRealBootBoundary(t *testing.T) {
	script, err := os.ReadFile("hosted-vm-boot-continuation-ordering.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, marker := range []string{
		"hosted_vm_reboot",
		"explicit_session.current_boot",
		"autostart.manifest_exact_while_active",
		"same_boot_restart.session_unchanged",
		"same_boot_restart.no_boot_attempt",
		"boot_autostart.new_session",
		"boot_autostart.current_boot_session",
		"boot_autostart.generation_exact",
		"podlaz.boot-autostart-attempt.v1",
		"podlaz.network-session-state.v1",
		"candidate.provenance_after_reboot",
		"hosted_vm_tun_capture_exact_active_authority yes",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("boot continuation ordering scenario missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		"boot_continuation_prepare_simulated_later_boot",
		"make-manifest-eligible",
		"rm -f /run/podlaz/boot-autostart-attempt.json",
		"rm -f /run/podlaz/network-session-continuation.json",
		"ip link del podlaz0",
		"systemd-nspawn",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("boot continuation ordering scenario contains forbidden repair/simulation %q", forbidden)
		}
	}
}

func TestHostedVMBootContinuationOrderingUsesPersistentForeignFixture(t *testing.T) {
	script, err := os.ReadFile("hosted-vm-boot-continuation-ordering.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, marker := range []string{
		"podlaz-vm-foreign.service",
		"WantedBy=multi-user.target",
		"pzvm_foreign",
		"pzvmforeign0",
		"192.0.2.1/32",
		"boot_autostart.foreign_state",
		"fixture.foreign_state_terminal",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("boot continuation ordering foreign-state fixture missing %q", marker)
		}
	}
}

func TestHostedVMBootContinuationOrderingWorkflowIsFocused(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/hosted-vm-boot-continuation-ordering.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(workflow))
	for _, marker := range []string{
		"ubuntu-24.04",
		"qemu-system-x86",
		"hosted-vm-boot-continuation-ordering.sh",
		"real reboot orders continuation before fresh autostart",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("boot continuation ordering workflow missing %q", marker)
		}
	}
	for _, forbidden := range []string{"suspend", "wifi", "package upgrade", "real-provider", "terminal boot"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("boot continuation ordering workflow mixes another lifecycle domain %q", forbidden)
		}
	}
}
