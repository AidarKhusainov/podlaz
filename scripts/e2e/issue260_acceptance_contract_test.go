package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestIssue260AcceptanceOccupiesHistoricalResourcesWithoutForeignVPNAdapters(t *testing.T) {
	data, err := os.ReadFile("issue260-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read issue260 acceptance: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		`FOREIGN_TUN_CIDR="198.18.0.1/32"`,
		`FOREIGN_TABLE="51820"`,
		`FOREIGN_RULE_PRIORITY_A="9999"`,
		`FOREIGN_RULE_PRIORITY_B="10000"`,
		"ip tuntap add",
		"collision_free_allocation",
		"protected_data_plane",
		"assert_foreign_fixture after_disconnect",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("issue 260 acceptance must contain %q", required)
		}
	}
	for _, forbidden := range []string{"nmcli connection down", "wireguard", "openvpn", "mullvad", "protonvpn", "throne"} {
		if strings.Contains(strings.ToLower(script), forbidden) {
			t.Fatalf("issue 260 acceptance must not contain product-specific foreign VPN control %q", forbidden)
		}
	}
}

func TestIssue260AcceptanceProvesPersistedAllocationAndBaselineSurvival(t *testing.T) {
	data, err := os.ReadFile("issue260-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read issue260 acceptance: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"assert_dynamic_transaction_allocation()",
		"desired_plan",
		"tun_address",
		"routes",
		`step.get("kind") != "policy-rule"`,
		`address == "198.18.0.1/32"`,
		`"51820" in tables`,
		`value in (9999, 10000)`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("allocation proof must inspect %q", required)
		}
	}
	baseline := shellFunctionBody(t, script, "assert_foreign_fixture")
	for _, required := range []string{"FOREIGN_TUN", "FOREIGN_TUN_CIDR", "FOREIGN_ROUTE", "FOREIGN_RULE_PRIORITY_A", "FOREIGN_RULE_PRIORITY_B", "FOREIGN_NFT_TABLE", "FOREIGN_DNS_LINK"} {
		if !strings.Contains(baseline, required) {
			t.Fatalf("baseline survival proof must inspect %q", required)
		}
	}
}
