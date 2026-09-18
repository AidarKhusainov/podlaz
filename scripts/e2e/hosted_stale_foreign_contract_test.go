package e2e_test

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const (
	hostedSafetyWorkflow     = "../../.github/workflows/hosted-recovery.yml"
	hostedStaleObservation   = "hosted-stale-observation.sh"
	hostedForeignStateSafety = "hosted-foreign-state-safety.sh"
)

func readHostedSafetyFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireHostedSafetyMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted stale/foreign contract lost %q", marker)
		}
	}
}

func forbidHostedSafetyMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			t.Fatalf("hosted stale/foreign contract contains forbidden %q", marker)
		}
	}
}

func TestHostedStaleObservationAndForeignStateSafetyWorkflowKeepsScenariosIndependent(t *testing.T) {
	workflow := readHostedSafetyFile(t, hostedSafetyWorkflow)
	requireHostedSafetyMarkers(t, workflow,
		"stale-observation:",
		"name: Stale observation convergence",
		"go test ./scripts/e2e -run '^TestHostedStaleObservationAndForeignStateSafety' -count=1",
		"bash scripts/e2e/hosted-stale-observation.sh",
		"hosted-stale-observation.txt",
		"podlaz-hosted-stale-observation",
		"foreign-state-safety:",
		"name: Foreign state safety",
		"bash scripts/e2e/hosted-foreign-state-safety.sh",
		"hosted-foreign-state-safety.txt",
		"podlaz-hosted-foreign-state-safety",
	)
	for _, action := range []string{"actions/checkout", "actions/setup-go", "actions/upload-artifact"} {
		re := regexp.MustCompile(regexp.QuoteMeta("uses: "+action+"@") + `[0-9a-f]{40}`)
		if !re.MatchString(workflow) {
			t.Fatalf("%s must remain pinned to an immutable 40-hex commit", action)
		}
	}
	forbidHostedSafetyMarkers(t, workflow,
		"self-hosted",
		"${{ secrets.",
		"PODLAZ_E2E_PROFILE_URI",
	)
}

func TestHostedStaleObservationUsesExistingMissingLinkPathWithoutRepair(t *testing.T) {
	script := readHostedSafetyFile(t, hostedStaleObservation)
	requireHostedSafetyMarkers(t, script,
		`BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		`PODLAZ_E2E_TUN_HOOKS=true`,
		`PODLAZ_E2E_TUN_HOOK_PHASE=dns-missing-link-rollback`,
		"dns-missing-link.ready",
		"dns-missing-link.continue",
		"dns-rollback.exit-code",
		"dns-rollback.stdout",
		"dns-rollback.stderr",
		"verify_resolvectl_missing_link.py",
		"network-verify",
		"network_verify_failure",
		"rollback_status",
		"assert_observation_only_foreign_link",
		"recover --execute --yes --json",
		"observation.never_authority",
		"resolver.missing_link_classified",
		"resolver.bounded_convergence",
		"foreign.state_preserved",
	)
	forbidHostedSafetyMarkers(t, script,
		"ip address add 198.18.",
		"ip route add default dev podlaz0",
		"resolvectl dns podlaz0",
		"nft add table inet podlaz",
	)
	cmd := exec.Command("bash", "-n", hostedStaleObservation)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedStaleObservation, err, output)
	}
}

func TestHostedForeignStateSafetyProvesOccupiedAllocationAndBroadCoexistence(t *testing.T) {
	script := readHostedSafetyFile(t, hostedForeignStateSafety)
	requireHostedSafetyMarkers(t, script,
		`BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		`FOREIGN_TUN_CIDR="198.18.0.1/32"`,
		`FOREIGN_TABLE="51820"`,
		`FOREIGN_RULE_PRIORITY_A="9999"`,
		`FOREIGN_RULE_PRIORITY_B="10000"`,
		"resolvectl dns",
		"nft add table",
		"systemd-run --unit=",
		"nmcli connection add",
		"assert_collision_free_allocation",
		"candidate.allocation_disjoint",
		"foreign.connect_preserved",
		"foreign.churn_preserved",
		"foreign.recovery_preserved",
		"foreign.cleanup_preserved",
		"recover --execute --yes --json",
		"verified-active",
		"terminal-clean",
	)
	forbidHostedSafetyMarkers(t, script,
		"rm -rf /run/podlaz",
		"ip route flush table all",
		"nft flush ruleset",
	)
	cmd := exec.Command("bash", "-n", hostedForeignStateSafety)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedForeignStateSafety, err, output)
	}
}

func TestHostedSyntheticBaseAllowsPersistedCollisionFreeAddress(t *testing.T) {
	base := readHostedSafetyFile(t, hostedSyntheticTUN)
	forbidHostedSafetyMarkers(t, base,
		"assert_tun_package_address_present active podlaz0 198.18.0.1/32",
	)
	requireHostedSafetyMarkers(t, base,
		"assert_persisted_tun_address_present",
		"desired_plan",
		"tun_address",
	)
}
