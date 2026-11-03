package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Execute the actual scenario control flow with guest operations stubbed out.
// A passing source-marker test alone cannot prove that Bash stops at a failed
// assertion instead of advancing to the next restart.
func TestHostedVMBootAuthorityRestartBatch(t *testing.T) {
	data, err := os.ReadFile("hosted-vm-boot-authority.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start := strings.Index(text, "run_active_restart_round() {")
	end := strings.Index(text, "\nmain() {")
	if start < 0 || end <= start {
		t.Fatal("missing bounded active-restart scenario")
	}
	functions := text[start:end]
	for _, tc := range []struct {
		name, rounds, stage        string
		failureRound, wantRestarts int
		wantSuccess                bool
	}{
		{"default-single", "1", "", 0, 1, true},
		{"bounded-three", "3", "", 0, 3, true},
		{"first-dns-failure", "3", "status", 1, 1, false},
		{"second-dns-failure", "3", "status", 2, 2, false},
		{"third-dns-failure", "3", "status", 3, 3, false},
		{"restart-command", "3", "restart", 2, 2, false},
		{"process-identity", "3", "pid", 2, 2, false},
		{"attempt-identity", "3", "attempt", 2, 2, false},
		{"generation-identity", "3", "generation", 2, 2, false},
		{"session-identity", "3", "session", 2, 2, false},
		{"exact-authority", "3", "authority", 2, 2, false},
		{"privacy-envelope", "3", "privacy", 2, 2, false},
		{"foreign-state", "3", "foreign", 2, 2, false},
		{"protected-traffic", "3", "traffic", 2, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := fmt.Sprintf("set -Eeuo pipefail\nACTIVE_RESTART_ROUNDS=%q\nFAIL_STAGE=%q\nFAIL_ROUND=%d\n", tc.rounds, tc.stage, tc.failureRound)
			program += restartBatchGuestStubs + "\n" + functions + "\nrun_scenario\nprintf 'scenario-complete\\n'\n"
			out, runErr := exec.Command("bash", "-c", program).CombinedOutput()
			got := string(out)
			if (runErr == nil) != tc.wantSuccess {
				t.Fatalf("success=%v want %v: %v\n%s", runErr == nil, tc.wantSuccess, runErr, got)
			}
			if n := strings.Count(got, "active-restart="); n != tc.wantRestarts {
				t.Fatalf("restarts=%d want %d\n%s", n, tc.wantRestarts, got)
			}
			if !tc.wantSuccess && (strings.Contains(got, "explicit-disconnect") || strings.Contains(got, "scenario-complete")) {
				t.Fatalf("failure advanced to later lifecycle assertions:\n%s", got)
			}
			if tc.stage == "status" && strings.Count(got, "dns_scope_cause=command-timeout") != 1 {
				t.Fatalf("first DNS failure evidence missing or repeated:\n%s", got)
			}
			if tc.wantSuccess && tc.rounds == "3" {
				for round := 1; round <= 3; round++ {
					for _, assertion := range []string{"status-180", "authority", "privacy", "foreign", "traffic"} {
						if !strings.Contains(got, fmt.Sprintf("%s=%d\n", assertion, round)) {
							t.Fatalf("round %d skipped %s:\n%s", round, assertion, got)
						}
					}
				}
			}
		})
	}
}

func TestHostedVMBootAuthorityRestartBudgetRejectsUnboundedInput(t *testing.T) {
	data, err := os.ReadFile("hosted-vm-boot-authority.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start := strings.Index(text, "configure_restart_rounds() {")
	if start < 0 {
		t.Fatal("missing restart-budget validation")
	}
	end := strings.Index(text[start:], "\n}\n")
	if end < 0 {
		t.Fatal("unterminated restart-budget validation")
	}
	fn := text[start : start+end+3]
	for _, rounds := range []string{"1", "2", "3", "0", "4", "100", "-1", "1+2", "03", "bad"} {
		t.Run(rounds, func(t *testing.T) {
			program := "set -Eeuo pipefail\nfail() { exit 2; }\nEVIDENCE_KEYS=()\n" + fn + "\nconfigure_restart_rounds\n"
			cmd := exec.Command("bash", "-c", program)
			cmd.Env = append(os.Environ(), "PODLAZ_E2E_ACTIVE_RESTART_ROUNDS="+rounds)
			out, runErr := cmd.CombinedOutput()
			want := rounds == "1" || rounds == "2" || rounds == "3"
			if (runErr == nil) != want {
				t.Fatalf("accepted=%v want %v: %s", runErr == nil, want, out)
			}
		})
	}
}

const restartBatchGuestStubs = `
CANDIDATE_DEB=fixture.deb EXPECTED_COMMIT=fixture XRAY_ROOT=fixture PRIVATE_ROOT=fixture REPO_ROOT=fixture
active_restarts=0
active=true
declare -A evidence=()
should_fail() { [[ "$active" == true && "$FAIL_STAGE" == "$1" && "$active_restarts" == "$FAIL_ROUND" ]]; }
mark_failure() { printf 'failure=%s.%s\n' "$1" "$2"; }
record_evidence() {
  [[ -z "${evidence[$1]+present}" ]] || { printf 'duplicate=%s\n' "$1"; exit 77; }
  evidence[$1]=1
  printf 'evidence=%s.%s\n' "$1" "$2"
}
for fn in hosted_vm_probe_acceleration hosted_vm_tun_init hosted_vm_tun_start_endpoint hosted_vm_prepare_image hosted_vm_start hosted_vm_wait_ssh hosted_vm_wait_cloud_init hosted_vm_tun_install_candidate hosted_vm_tun_prepare_control hosted_vm_tun_assert_candidate_provenance hosted_vm_tun_install_helpers hosted_vm_tun_install_polkit hosted_vm_tun_prepare_profile hosted_vm_tun_capture_direct_baseline hosted_vm_tun_assert_endpoint_reachable hosted_vm_tun_create_foreign_state hosted_vm_tun_run_podlaz assert_manifest_exact assert_attempt_absent hosted_vm_assert_candidate_provenance wait_succeeded_attempt hosted_vm_tun_assert_exact_terminal_cleanup assert_session_absent hosted_vm_tun_run_clean_recovery assert_public_artifact_privacy; do
  eval "$fn() { :; }"
done
hosted_vm_tun_profile_id() { printf profile; }
hosted_vm_boot_id() { printf boot-a; }
manifest_generation() { printf 0123456789abcdef0123456789abcdef; }
hosted_vm_reboot() { printf 'boot-a\tboot-b\n'; }
attempt_sha() { if should_fail attempt; then printf other; else printf attempt; fi; }
hosted_vm_tun_session_id() { if should_fail session; then printf other; else printf session; fi; }
assert_attempt_exact() { ! should_fail generation; }
hosted_vm_ga_bash() {
  case "$1" in
    'systemctl restart podlazd.service')
      if [[ "$active" == true ]]; then
        active_restarts=$((active_restarts+1))
        printf 'active-restart=%d\n' "$active_restarts"
        ! should_fail restart
      fi ;;
    'systemctl show -p MainPID --value podlazd.service')
      if should_fail pid; then printf '%s' "$((1000+active_restarts-1))"; else printf '%s' "$((1000+active_restarts))"; fi ;;
    *) : ;;
  esac
}
hosted_vm_tun_wait_status() {
  if [[ "$1" == verified-active && "$2" == 180 ]]; then
    [[ "${3:-}" == diagnose ]]
    printf 'status-180=%d\n' "$active_restarts"
    ! should_fail status
  fi
}
boot_restart_readonly_diagnostic() { printf 'boot-restart-diagnostic.dns_scope_cause=command-timeout\n'; }
daemon_restart_failure_token() { printf 'resume.connect-replay.failed.resolved-link-query-verify.none.completed.incomplete'; }
hosted_vm_tun_capture_exact_active_authority() { printf 'authority=%d\n' "$active_restarts"; ! should_fail authority; }
hosted_vm_tun_assert_direct_blocked() { printf 'privacy=%d\n' "$active_restarts"; ! should_fail privacy; }
hosted_vm_tun_assert_foreign_state() { printf 'foreign=%d\n' "$active_restarts"; ! should_fail foreign; }
hosted_vm_tun_run_traffic() { printf 'traffic=%d\n' "$active_restarts"; ! should_fail traffic; }
hosted_vm_tun_disconnect() { printf 'explicit-disconnect\n'; active=false; }
`

func TestHostedVMBootAuthorityReportRejectsMissingDiagnosticRound(t *testing.T) {
	data, err := os.ReadFile("hosted-vm-boot-authority.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	configStart, configEnd := strings.Index(text, "EVIDENCE_KEYS=("), strings.Index(text, "\nevidence_recorded() {")
	validateStart, validateEnd := strings.Index(text, "validate_report() {"), strings.Index(text, "\nvalidate_candidate() {")
	if configStart < 0 || configEnd <= configStart || validateStart < 0 || validateEnd <= validateStart {
		t.Fatal("missing report configuration/validator")
	}
	for _, tc := range []struct {
		name, rounds, mutation string
		wantCount              int
		wantSuccess            bool
	}{
		{"default-schema", "1", "", 33, true},
		{"all-rounds-proven", "3", "", 49, true},
		{"later-round-missing", "3", `sed -i '/^daemon_restart.round_2.traffic=/d' "$REPORT"`, 49, false},
		{"later-round-failed", "3", `sed -i 's/^daemon_restart.round_2.traffic=pass$/daemon_restart.round_2.traffic=fail/' "$REPORT"`, 49, false},
		{"duplicate-round-evidence", "3", `printf 'daemon_restart.round_2.traffic=pass\n' >>"$REPORT"`, 49, false},
		{"failure-not-erased", "3", `sed -i 's/^failure.class=none$/failure.class=product/' "$REPORT"`, 49, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := "set -Eeuo pipefail\nfail() { exit 2; }\n" + text[configStart:configEnd] + "\n" + text[validateStart:validateEnd] + "\nconfigure_restart_rounds\n"
			program += fmt.Sprintf("[[ ${#EVIDENCE_KEYS[@]} == %d ]] || exit 77\n", tc.wantCount)
			program += "printf '%s=pass\\n' \"${EVIDENCE_KEYS[@]}\" >\"$REPORT\"\nprintf 'capability.kvm=unavailable\\nfailure.class=none\\nfailure.step=none\\n' >>\"$REPORT\"\n" + tc.mutation + "\nvalidate_report\n"
			cmd := exec.Command("bash", "-c", program)
			cmd.Env = append(os.Environ(), "PODLAZ_E2E_ACTIVE_RESTART_ROUNDS="+tc.rounds, "REPORT="+t.TempDir()+"/report.txt")
			out, runErr := cmd.CombinedOutput()
			if (runErr == nil) != tc.wantSuccess {
				t.Fatalf("success=%v want %v: %v\n%s", runErr == nil, tc.wantSuccess, runErr, out)
			}
		})
	}
}

func TestBootReplayDiagnosticWorkflowHasOneBoundedJob(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/boot-replay-diagnostic.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{"diagnostics/boot-replay-budgeted", "timeout-minutes: 30", "PODLAZ_E2E_ACTIVE_RESTART_ROUNDS: '3'", "go test ./internal/network/executor ./internal/daemon ./scripts/e2e", "persist-credentials: false", "Validate public report", "Remove private VM state"} {
		if !strings.Contains(text, required) {
			t.Fatalf("diagnostic workflow missing %q", required)
		}
	}
	for _, forbidden := range []string{"pull_request:", "schedule:", "matrix:", "continue-on-error:", "rerun", "contents: write", "sudo bash scripts/e2e/"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unexpected diagnostic workflow expansion %q", forbidden)
		}
	}
	if strings.Count(text, "run: bash scripts/build-deb.sh") != 1 || strings.Count(text, "run: bash scripts/e2e/hosted-vm-boot-authority.sh \"${DEV_DEB}\"") != 1 {
		t.Fatal("diagnostic job must build once and provision exactly one scenario")
	}
}
