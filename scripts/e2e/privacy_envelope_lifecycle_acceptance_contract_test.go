package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestPrivacyEnvelopeLifecycleAcceptanceCoversPrivacyAndTerminalBoundaries(t *testing.T) {
	data, err := os.ReadFile("privacy-envelope-lifecycle-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read Privacy Envelope lifecycle acceptance: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"privacy_envelope_armed",
		"sudo -n systemctl kill --kill-who=main -s KILL podlazd.service",
		"assert_direct_uplink_blocked",
		"daemon_crash_recovered_without_manual_repair",
		"PODLAZ_E2E_TUN_TERMINAL_FAILURE",
		"terminal-failure.trigger",
		"terminal_failure_triggered",
		"terminal-data-plane-clean.ready",
		"assert_privacy_envelope_present",
		"terminal_disconnected",
		"terminal_no_auto_reconnect",
		"ordinary_network_after_terminal",
		"foreign_collision_survived",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("Privacy Envelope lifecycle acceptance must contain %q", required)
		}
	}
	if strings.Contains(script, "network-resource-isolation-disconnect") {
		t.Fatal("terminal scenario must be driven by a terminal revalidation failure, not explicit disconnect")
	}
}

func TestPrivacyEnvelopeLifecycleAcceptanceDoesNotRepairProductStateManually(t *testing.T) {
	data, err := os.ReadFile("privacy-envelope-lifecycle-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read Privacy Envelope lifecycle acceptance: %v", err)
	}
	lower := strings.ToLower(string(data))
	for _, forbidden := range []string{
		"podlaz recover --execute",
		"ip rule del",
		"ip route del",
		"resolvectl revert podlaz0",
		"nft delete table inet podlaz_pe_",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("Privacy Envelope lifecycle success path must not contain manual product-state repair %q", forbidden)
		}
	}
}
