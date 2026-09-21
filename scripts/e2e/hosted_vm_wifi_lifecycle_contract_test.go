package e2e

import (
	"os"
	"strings"
	"testing"
)

func TestHostedVMWiFiLifecycleOwnsWiFiAssociationBoundary(t *testing.T) {
	script, err := os.ReadFile("hosted-vm-wifi-lifecycle.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, marker := range []string{
		"source \"${SCRIPT_DIR}/lib/hosted_vm.sh\"",
		"source \"${SCRIPT_DIR}/lib/hosted_vm_tun.sh\"",
		"mac80211_hwsim",
		"hostapd",
		"NetworkManager.service",
		"nmcli --wait 20 connection down",
		"nmcli --wait 30 connection up",
		"wifi.associated_before_tun",
		"wifi.disconnected",
		"wifi.reassociated",
		"privacy.envelope_retained",
		"privacy.direct_uplink_blocked",
		"tun.same_network_session",
		"tun.exact_authority_after_reconnect",
		"fixture.foreign_state_after_reconnect",
		"tun.traffic_after_reconnect",
		"tun.exact_terminal_cleanup",
		"guest.ordinary_connectivity_restored",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted VM Wi-Fi lifecycle scenario is missing %q", marker)
		}
	}

	for _, forbidden := range []string{
		"systemctl restart podlazd",
		"ip link del podlaz0",
		"rm -f /run/podlaz/network-session-continuation.json",
		"PODLAZ_E2E_TUN_HOOK",
		"PODLAZ_E2E_TUN_RECONCILIATION",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("hosted VM Wi-Fi lifecycle contains forbidden product repair/hook marker %q", forbidden)
		}
	}
}

func TestHostedVMWiFiLifecycleKeepsControlOffTestedWiFi(t *testing.T) {
	script, err := os.ReadFile("hosted-vm-wifi-lifecycle.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, marker := range []string{
		"management_if=",
		"ip netns add pzwifiap",
		"iw phy \"${ap_phy}\" set netns",
		"198.51.100.1/24",
		"172.31.254.2/30",
		"WIFI_ENDPOINT_IP=203.0.113.10",
		"ip address add \"${endpoint_ip}/32\" dev lo",
		"ip rule add priority \"${policy_priority}\"",
		"ip route del default via \"${management_gateway}\"",
		"ip -4 route show default | grep -F \"via 198.51.100.1 dev ${client_if}\"",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted VM Wi-Fi topology is missing %q", marker)
		}
	}
}

func TestHostedVMWiFiLifecycleWorkflowIsFocused(t *testing.T) {
	workflow, err := os.ReadFile("../../.github/workflows/hosted-vm-wifi-lifecycle.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ToLower(string(workflow))
	for _, marker := range []string{
		"name: hosted vm wi-fi lifecycle",
		"ubuntu-24.04",
		"qemu-system-x86",
		"hosted-vm-wifi-lifecycle.sh",
		"simulated wi-fi disconnect and reassociation",
	} {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted VM Wi-Fi lifecycle workflow is missing %q", marker)
		}
	}
	for _, forbidden := range []string{
		"self-hosted",
		"${{ secrets.",
		"suspend",
		"autostart",
		"package upgrade",
		"real-provider",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("hosted VM Wi-Fi lifecycle workflow mixes another evidence domain: %q", forbidden)
		}
	}
}
