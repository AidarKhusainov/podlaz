package e2e_test

import (
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHostedE2ECapabilitySyntheticVLESSLinkIsPrivate(t *testing.T) {
	tmp := t.TempDir()
	command := `
set -euo pipefail
export PODLAZ_E2E_CAPABILITY_SOURCE_ONLY=true
export E2E_TMP_ROOT="$1/private"
export E2E_ARTIFACT_DIR="$1/public"
mkdir -p "$E2E_TMP_ROOT" "$E2E_ARTIFACT_DIR"
source ./hosted-e2e-capability.sh
printf '%s\n' "$CAPABILITY_NETWORK_CIDR" "$CAPABILITY_HOST_CIDR" "$CAPABILITY_GUEST_CIDR"
`
	cmd := exec.Command("bash", "-c", command, "bash", tmp)
	cmd.Dir = "."
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("read hosted capability link contract: %v\n%s", err, output)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 3 {
		t.Fatalf("hosted capability link contract returned %d lines, want 3: %q", len(lines), output)
	}
	network, err := netip.ParsePrefix(lines[0])
	if err != nil {
		t.Fatalf("parse capability network %q: %v", lines[0], err)
	}
	if network.Bits() != 30 || !network.Addr().Is4() || !network.Addr().IsPrivate() {
		t.Fatalf("synthetic VLESS link network must be a private IPv4 /30, got %s", network)
	}

	for _, value := range lines[1:] {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			t.Fatalf("parse capability endpoint %q: %v", value, err)
		}
		if prefix.Bits() != 30 || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() {
			t.Fatalf("synthetic VLESS link endpoint must be a private IPv4 /30, got %s", prefix)
		}
		if prefix.Masked() != network.Masked() {
			t.Fatalf("synthetic VLESS endpoint %s is outside network %s", prefix, network)
		}
	}
}

func TestHostedE2ECapabilityChecksSyntheticLinkCollisionBeforeGuestStart(t *testing.T) {
	data, err := os.ReadFile(hostedCapabilityScript)
	if err != nil {
		t.Fatalf("read hosted capability script: %v", err)
	}
	script := string(data)
	if !strings.Contains(script, "assert_capability_subnet_available()") {
		t.Fatal("hosted capability must define an exact synthetic-link subnet collision check")
	}
	collisionCheck := strings.Index(script, "\n  assert_capability_subnet_available\n")
	guestStart := strings.Index(script, "sudo -n systemd-nspawn")
	if collisionCheck < 0 {
		t.Fatal("hosted capability must invoke the synthetic-link subnet collision check")
	}
	if guestStart < 0 {
		t.Fatal("hosted capability must start the disposable system guest with systemd-nspawn")
	}
	if collisionCheck > guestStart {
		t.Fatal("synthetic-link subnet collision must be checked before systemd-nspawn can mutate host networking")
	}

	for _, required := range []string{
		"CAPABILITY_NETWORK_CIDR",
		"ip -j -4 addr show",
		"ip -j -4 route show table all",
		"overlaps",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("synthetic-link collision check must account for %q", required)
		}
	}
}
