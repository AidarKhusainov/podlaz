package e2e_test

import (
	"os/exec"
	"strings"
	"testing"
)

const (
	hostedStaleObservation   = "hosted-stale-observation.sh"
	hostedForeignStateSafety = "hosted-foreign-state-safety.sh"
)

func TestHostedOwnershipSafetyWorkflowKeepsConditionsIndependent(t *testing.T) {
	workflow := readHostedRecoveryFile(t, hostedRecoveryWorkflow)
	requireHostedRecoveryMarkers(t, workflow,
		"stale-observation-safety:",
		"name: Stale link and resolver observation safety",
		"bash scripts/e2e/hosted-stale-observation.sh",
		"podlaz-hosted-stale-observation",
		"foreign-state-safety:",
		"name: Foreign network state and occupied identity safety",
		"bash scripts/e2e/hosted-foreign-state-safety.sh",
		"podlaz-hosted-foreign-state-safety",
	)
}

func TestHostedStaleObservationUsesSupportedMissingLinkRollback(t *testing.T) {
	hostedStaleObservation   = "hosted-stale-observation.sh"
	requireHostedRecoveryMarkers(t, script,
		`BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		"PODLAZ_E2E_HOSTED_EXPECT_CONNECT_FAILURE=true",
		"PODLAZ_E2E_TUN_HOOKS=true",
		"PODLAZ_E2E_TUN_HOOK_PHASE=dns-missing-link-rollback",
		"dns-missing-link.ready",
		"dns-missing-link.continue",
		"dns-rollback.exit-code",
		"dns-rollback.stdout",
		"dns-rollback.stderr",
		"verify_resolvectl_missing_link.py",
		"diagnostics-persisted",
		"rollback-started",
		"dns-rollback-started",
		"dns-rollback-result-captured",
		"rollback-completed",
		"ip link del dev podlaz0",
		"stale.resolved_missing_link=pass",
		"stale.rollback_converged=pass",
		"stale.observation_not_authority=pass",
		"stale.retry_verified_active=pass",
		"stale.retry_terminal_cleanup=pass",
	)
	if strings.Count(script, "ip link del dev podlaz0") != 1 {
		t.Fatal("stale observation qualification must inject exactly one link-loss fault")
	}
	forbidHostedRecoveryMarkers(t, script,
		"recover --execute",
		"ip route flush",
		"ip -4 route flush",
		"nft flush ruleset",
		"rm -rf /run/podlaz/transactions",
		"rm -f /run/podlaz/network-session-continuation.json",
	)
	hostedStaleObservation   = "hosted-stale-observation.sh"
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedStaleObservation, err, output)
	}
}

func TestHostedForeignStateSafetyCoversFullFixtureAndAllocation(t *testing.T) {
	hostedForeignStateSafety = "hosted-foreign-state-safety.sh"
	requireHostedRecoveryMarkers(t, script,
		`BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		`PODLAZ_E2E_HOSTED_CONTROL_PHASES="guest-ready verified-active terminal-clean recovery-clean"`,
		"198.18.0.1/32",
		"51820",
		"9999",
		"10000",
		"resolvectl dns",
		"resolvectl domain",
		"nft add table",
		"systemd-run",
		"nmcli connection add",
		"assert_collision_free_allocation",
		"nmcli connection down synthetic-uplink",
		"nmcli connection up synthetic-uplink",
		"foreign.connect_preserved=pass",
		"foreign.churn_preserved=pass",
		"foreign.cleanup_preserved=pass",
		"foreign.recovery_preserved=pass",
		"allocation.disjoint=pass",
	)
	forbidHostedRecoveryMarkers(t, script,
		"recover --execute",
		"ip route flush",
		"ip -4 route flush",
		"nft flush ruleset",
		"rm -rf /run/podlaz/transactions",
		"rm -f /run/podlaz/network-session-continuation.json",
	)
	hostedForeignStateSafety = "hosted-foreign-state-safety.sh"
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedForeignStateSafety, err, output)
	}
}

func TestHostedSyntheticTUNPositiveControlIsAllocationAware(t *testing.T) {
	base := readHostedRecoveryFile(t, hostedSyntheticTUN)
	requireHostedRecoveryMarkers(t, base,
		"assert_active_allocated_address",
		"hosted_control_pause recovery-clean",
	)
	forbidHostedRecoveryMarkers(t, base,
		"assert_tun_package_address_present active podlaz0 198.18.0.1/32",
	)
}
