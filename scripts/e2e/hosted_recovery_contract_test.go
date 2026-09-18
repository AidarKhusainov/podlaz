package e2e_test

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const (
	hostedRecoveryWorkflow = "../../.github/workflows/hosted-recovery.yml"
	hostedDaemonRecovery   = "hosted-daemon-recovery.sh"
	hostedSyntheticTUN     = "hosted-synthetic-tun.sh"
)

func readHostedRecoveryFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireHostedRecoveryMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted recovery contract lost %q", marker)
		}
	}
}

func forbidHostedRecoveryMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			t.Fatalf("hosted recovery contract contains forbidden %q", marker)
		}
	}
}

func TestHostedDaemonRecoveryWorkflowIsThinIsolatedQualification(t *testing.T) {
	workflow := readHostedRecoveryFile(t, hostedRecoveryWorkflow)
	requireHostedRecoveryMarkers(t, workflow,
		"name: Hosted Recovery",
		"pull_request:",
		"workflow_dispatch:",
		"runs-on: ubuntu-24.04",
		"persist-credentials: false",
		"CANDIDATE_COMMIT: ${{ github.sha }}",
		"PODLAZ_COMMIT: ${{ env.CANDIDATE_COMMIT }}",
		"PODLAZ_E2E_CANDIDATE_COMMIT: ${{ env.CANDIDATE_COMMIT }}",
		"bash scripts/build-deb.sh",
		"go test ./scripts/e2e -run '^TestHostedDaemonRecovery' -count=1",
		"bash scripts/e2e/hosted-daemon-recovery.sh",
		"hosted-daemon-recovery.txt",
	)
	for _, action := range []string{"actions/checkout", "actions/setup-go", "actions/upload-artifact"} {
		re := regexp.MustCompile(regexp.QuoteMeta("uses: "+action+"@") + `[0-9a-f]{40}`)
		if !re.MatchString(workflow) {
			t.Fatalf("%s must be pinned to an immutable 40-hex commit", action)
		}
	}
	forbidHostedRecoveryMarkers(t, workflow,
		"self-hosted",
		"${{ secrets.",
		"PODLAZ_E2E_PROFILE_URI",
		"machinectl ",
		"systemd-nspawn ",
		"systemctl kill",
		"nft ",
	)
}

func TestHostedDaemonRecoveryUsesExplicitSyntheticControlBoundaries(t *testing.T) {
	base := readHostedRecoveryFile(t, hostedSyntheticTUN)
	requireHostedRecoveryMarkers(t, base,
		`HOSTED_CONTROL_DIR="${PODLAZ_E2E_HOSTED_CONTROL_DIR:-}"`,
		`HOSTED_CONTROL_TIMEOUT_SECONDS="${PODLAZ_E2E_HOSTED_CONTROL_TIMEOUT_SECONDS:-180}"`,
		"hosted_control_pause()",
		`hosted_control_pause candidate-ready`,
		`hosted_control_pause verified-active`,
		`hosted_control_pause terminal-clean`,
		`.ready`,
		`.continue`,
	)

	script := readHostedRecoveryFile(t, hostedDaemonRecovery)
	requireHostedRecoveryMarkers(t, script,
		`PODLAZ_E2E_HOSTED_CONTROL_DIR="${CONTROL_DIR}"`,
		`PODLAZ_E2E_HOSTED_CONTROL_PHASES="candidate-ready verified-active terminal-clean"`,
		`wait_for_control_ready candidate-ready`,
		`release_control candidate-ready`,
		`wait_for_control_ready verified-active`,
		`release_control verified-active`,
		`wait_for_control_ready terminal-clean`,
		`release_control terminal-clean`,
	)
	forbidHostedRecoveryMarkers(t, script,
		"kill -STOP",
		"kill -CONT",
		"wait_for_base_marker",
		"pause_base_at_marker",
	)
}

func TestHostedDaemonRecoveryReusesSyntheticSubstrateAndInjectsOneFailureDomain(t *testing.T) {
	script := readHostedRecoveryFile(t, hostedDaemonRecovery)
	requireHostedRecoveryMarkers(t, script,
		`BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		`MACHINE="podlaz-synthetic-tun"`,
		`FOREIGN_NFT_TABLE="pzsynt_foreign"`,
		`network-session-continuation.json`,
		`RestartSec=`,
		`systemctl kill --kill-who=main -s KILL podlazd.service`,
		`assert_privacy_envelope_present`,
		`assert_direct_uplink_blocked`,
		`assert_foreign_sentinel`,
		`wait_for_verified_active`,
		`assert_daemon_replaced`,
		`assert_inactive_authority_clean`,
		`tun.verified_active=pass`,
		`tun.terminal_cleanup=pass`,
		`hosted-synthetic-tun.txt`,
	)
	forbidHostedRecoveryMarkers(t, script,
		"qemu-system",
		"systemctl suspend",
		"reboot",
		"PODLAZ_E2E_PROFILE_URI",
		"apt-get install -y '${GUEST_CANDIDATE}'",
		"systemd-nspawn --",
	)
	cmd := exec.Command("bash", "-n", hostedDaemonRecovery)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedDaemonRecovery, err, output)
	}
}

func TestHostedDaemonRecoveryProvesInactiveRestartBeforeCrashInjection(t *testing.T) {
	script := readHostedRecoveryFile(t, hostedDaemonRecovery)
	inactive := strings.Index(script, "mark_failure product daemon.inactive_restart")
	crash := strings.Index(script, "mark_failure product daemon.crash")
	if inactive < 0 || crash < 0 {
		t.Fatal("hosted daemon recovery lost inactive restart or crash boundary")
	}
	if inactive >= crash {
		t.Fatal("inactive/no-authority restart must be proven before the active daemon crash blocker")
	}
}

func TestHostedDaemonRecoveryEvidenceIsBoundedToSameBootPrivacyContinuation(t *testing.T) {
	script := readHostedRecoveryFile(t, hostedDaemonRecovery)
	for _, key := range []string{
		"candidate.positive_control",
		"daemon.crash_injected",
		"privacy.envelope_retained",
		"privacy.direct_uplink_blocked",
		"foreign.nft_preserved",
		"daemon.same_boot_resumed",
		"daemon.identity_replaced",
		"daemon.inactive_restart_clean",
		"base.terminal_cleanup",
		"artifact.privacy",
	} {
		if !strings.Contains(script, key) {
			t.Fatalf("hosted daemon recovery evidence schema lost %q", key)
		}
	}
	forbidHostedRecoveryMarkers(t, script,
		"boot_id_changed",
		"suspend",
		"wifi",
		"roaming",
		"historical_upgrade",
	)
}
