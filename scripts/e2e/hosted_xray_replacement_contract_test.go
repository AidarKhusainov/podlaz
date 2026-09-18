package e2e_test

import (
	"os"
	"os/exec"
	"regexp"
	"testing"
)

const hostedXrayReplacement = "hosted-xray-replacement.sh"

func TestHostedXrayReplacementWorkflowIsSeparateHostedJob(t *testing.T) {
	workflow := readHostedRecoveryFile(t, hostedRecoveryWorkflow)
	requireHostedRecoveryMarkers(t, workflow,
		"xray-replacement:",
		"name: Xray replacement privacy",
		"runs-on: ubuntu-24.04",
		"persist-credentials: false",
		"go test ./scripts/e2e -run '^TestHostedXrayReplacement' -count=1",
		"bash scripts/e2e/hosted-xray-replacement.sh",
		"hosted-xray-replacement.txt",
	)
	for _, action := range []string{"actions/checkout", "actions/setup-go", "actions/upload-artifact"} {
		re := regexp.MustCompile(regexp.QuoteMeta("uses: "+action+"@") + `[0-9a-f]{40}`)
		if !re.MatchString(workflow) {
			t.Fatalf("%s must remain pinned to an immutable 40-hex commit", action)
		}
	}
}

func TestHostedSyntheticTunOffersOptInGuestReadyBoundary(t *testing.T) {
	base := readHostedRecoveryFile(t, hostedSyntheticTUN)
	requireHostedRecoveryMarkers(t, base,
		`HOSTED_CONTROL_PHASES="${PODLAZ_E2E_HOSTED_CONTROL_PHASES:-verified-active terminal-clean}"`,
		"hosted_control_phase_enabled()",
		"hosted_control_pause guest-ready",
	)
}

func TestHostedXrayReplacementUsesExactTrackedChildAndExistingRebuildSeam(t *testing.T) {
	data, err := os.ReadFile(hostedXrayReplacement)
	if err != nil {
		t.Fatalf("read %s: %v", hostedXrayReplacement, err)
	}
	script := string(data)
	requireHostedRecoveryMarkers(t, script,
		`BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		`MACHINE="podlaz-synthetic-tun"`,
		`XRAY_HOOK_DIR="/run/podlaz-hosted-xray-replacement-hooks"`,
		`PODLAZ_E2E_HOSTED_CONTROL_PHASES="guest-ready verified-active terminal-clean"`,
		`wait_for_control_ready guest-ready`,
		`release_control guest-ready`,
		`PODLAZ_E2E_TUN_TERMINAL_FAILURE=true`,
		`PODLAZ_E2E_TUN_RECONCILIATION_REBUILD_PAUSE=true`,
		`PODLAZ_E2E_TUN_ROLLBACK_PAUSE=true`,
		`rollback-pause.arm`,
		`rollback-pause.ready`,
		`diagnose_rollback_pause()`,
		`live-link`,
		`ifindex-match`,
		`mtu-match`,
		`subprocess.run`,
		`release_rollback_pause`,
		`reconciliation-rebuild.ready`,
		`reconciliation-rebuild.continue`,
		`child_processes`,
		`start_time`,
		`/proc/${pid}/stat`,
		`kill -KILL`,
		`assert_privacy_envelope_present`,
		`assert_direct_uplink_blocked`,
		`assert_foreign_sentinel`,
		`wait_for_verified_active`,
		`TUN_DIAGNOSTIC="/run/podlaz/diagnostics/tun-last.json"`,
		`diagnose_rebuild_failure()`,
		`diagnose-rebuild`,
		`mark_failure product "xray.rebuild_resume.${diagnosis}"`,
		`assert_xray_replaced`,
		`candidate.positive_control`,
		`xray.crash_injected`,
		`privacy.envelope_retained`,
		`privacy.direct_uplink_blocked`,
		`foreign.nft_preserved`,
		`xray.generation_replaced`,
		`xray.same_session_verified`,
		`base.terminal_cleanup`,
		`artifact.privacy`,
	)
	forbidHostedRecoveryMarkers(t, script,
		`XRAY_HOOK_DIR="/tmp/`,
		`.get("failure_reason")`,
		`print(result.stdout)`,
		`print(result.stderr)`,
		"systemctl restart podlazd.service",
		"systemctl suspend",
		"rtcwake",
		"reboot",
		"PODLAZ_E2E_PROFILE_URI",
		"qemu-system",
	)
	cmd := exec.Command("bash", "-n", hostedXrayReplacement)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedXrayReplacement, err, output)
	}
}
