package e2e_test

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const (
	hostedFaultWorkflow      = "../../.github/workflows/hosted-recovery.yml"
	hostedNetworkApplyFault  = "hosted-network-apply-rollback.sh"
	hostedNetworkVerifyFault = "hosted-network-verify-rollback.sh"
	hostedFaultHelper        = "lib/hosted_fault_rollback.sh"
)

func readHostedFaultFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireHostedFaultMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted fault rollback contract lost %q", marker)
		}
	}
}

func forbidHostedFaultMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			t.Fatalf("hosted fault rollback contract contains forbidden %q", marker)
		}
	}
}

func TestHostedFaultRollbackWorkflowKeepsApplyAndVerifyIndependent(t *testing.T) {
	workflow := readHostedFaultFile(t, hostedFaultWorkflow)
	requireHostedFaultMarkers(t, workflow,
		"network-apply-rollback:",
		"name: Network apply rollback diagnostics",
		"go test ./scripts/e2e -run '^TestHostedFaultRollback' -count=1",
		"bash scripts/e2e/hosted-network-apply-rollback.sh",
		"hosted-network-apply-rollback.txt",
		"podlaz-hosted-network-apply-rollback",
		"network-verify-rollback:",
		"name: Network verify rollback diagnostics",
		"bash scripts/e2e/hosted-network-verify-rollback.sh",
		"hosted-network-verify-rollback.txt",
		"podlaz-hosted-network-verify-rollback",
	)
	for _, action := range []string{"actions/checkout", "actions/setup-go", "actions/upload-artifact"} {
		re := regexp.MustCompile(regexp.QuoteMeta("uses: "+action+"@") + `[0-9a-f]{40}`)
		if !re.MatchString(workflow) {
			t.Fatalf("%s must remain pinned to an immutable 40-hex commit", action)
		}
	}
	forbidHostedFaultMarkers(t, workflow,
		"self-hosted",
		"${{ secrets.",
		"PODLAZ_E2E_PROFILE_URI",
		"machinectl ",
		"systemd-nspawn ",
		"ip link del podlaz0",
		"nft delete table inet podlaz",
	)
}

func TestHostedFaultRollbackScenariosAreThinDistinctEntrypoints(t *testing.T) {
	cases := []struct {
		path           string
		phase          string
		classification string
		event          string
		report         string
	}{
		{hostedNetworkApplyFault, "tun-address-apply", "tun_address_apply_failure", "tun-address-apply-injected", "hosted-network-apply-rollback.txt"},
		{hostedNetworkVerifyFault, "network-verify", "network_verify_failure", "network-verify-injected", "hosted-network-verify-rollback.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.phase, func(t *testing.T) {
			script := readHostedFaultFile(t, tc.path)
			requireHostedFaultMarkers(t, script,
				`source "${SCRIPT_DIR}/lib/hosted_fault_rollback.sh"`,
				`FAULT_PHASE="`+tc.phase+`"`,
				`FAULT_CLASSIFICATION="`+tc.classification+`"`,
				`FAULT_EVENT="`+tc.event+`"`,
				`REPORT_BASENAME="`+tc.report+`"`,
				"run_hosted_fault_rollback",
			)
			forbidHostedFaultMarkers(t, script,
				"systemd-nspawn --",
				"ip link del podlaz0",
				"ip rule del",
				"nft delete table inet podlaz",
			)
			cmd := exec.Command("bash", "-n", tc.path)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("bash -n %s: %v\n%s", tc.path, err, output)
			}
		})
	}
}

func TestHostedFaultRollbackSharedHelperUsesExistingFaultHooksAndExactDiagnostics(t *testing.T) {
	helper := readHostedFaultFile(t, hostedFaultHelper)
	requireHostedFaultMarkers(t, helper,
		`BASE_SCENARIO="${HOSTED_FAULT_E2E_DIR}/hosted-synthetic-tun.sh"`,
		"PODLAZ_E2E_TUN_HOOKS=true",
		"PODLAZ_E2E_TUN_HOOK_PHASE=",
		"PODLAZ_E2E_TUN_HOOK_DIR=",
		"candidate-ready",
		"connect-failed",
		"/run/podlaz/diagnostics/tun-last.json",
		"events.log",
		"diagnostics-persisted",
		"rollback-started",
		"rollback-completed",
		"assert_failure_report",
		"assert_event_order",
		"assert_foreign_sentinel",
		"assert_owned_state_absent",
		"assert_clean_recovery",
		"FAILURE_CLASS=none",
		"FAILURE_STEP=none",
	)
	forbidHostedFaultMarkers(t, helper,
		"ip link del podlaz0",
		"ip rule del",
		"ip route del",
		"resolvectl revert podlaz0",
		"nft delete table inet podlaz",
		"recover --execute",
	)
}

func TestHostedFaultRollbackWaitsForDaemonReadinessAfterHookRestart(t *testing.T) {
	helper := readHostedFaultFile(t, hostedFaultHelper)
	requireHostedFaultMarkers(t, helper,
		"wait_for_fault_daemon_ready()",
		"systemctl is-active --quiet podlazd.service",
		`test -S "${DAEMON_SOCKET}"`,
		"wait_for_fault_daemon_ready || return 1",
	)
}

func TestHostedFaultRollbackBaseExposesOnlyOptInExpectedConnectFailurePause(t *testing.T) {
	base := readHostedFaultFile(t, hostedSyntheticTUN)
	requireHostedFaultMarkers(t, base,
		`HOSTED_EXPECT_CONNECT_FAILURE="${PODLAZ_E2E_HOSTED_EXPECT_CONNECT_FAILURE:-false}"`,
		`if (( connect_code != 0 )) && [[ "${HOSTED_EXPECT_CONNECT_FAILURE}" == true ]]; then`,
		"hosted_control_pause connect-failed",
	)
}
