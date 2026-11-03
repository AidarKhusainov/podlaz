package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHostedVMBootAuthorityRestartBudgetIsClosed(t *testing.T) {
	for _, value := range []string{"", "1", "2", "3", "0", "4", "-1", "03", " 3", "3\n", "$(touch injected)", "private.example.invalid"} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			out, trace, err := runBootRestartExperimentShell(t, value, "", 0, "boot_restart_cycles")
			valid := value == "" || value == "1" || value == "2" || value == "3"
			if valid {
				want := value
				if want == "" {
					want = "1"
				}
				if err != nil || strings.TrimSpace(out) != want {
					t.Fatalf("budget result=%q err=%v; want %s", out, err, want)
				}
			} else if err == nil {
				t.Fatalf("accepted invalid budget %q", value)
			}
			if trace != "" || strings.Contains(out, "private.example.invalid") || strings.Contains(out, "touch injected") {
				t.Fatalf("budget validation performed work or exposed input: out=%q trace=%q", out, trace)
			}
		})
	}
}

func TestHostedVMBootAuthorityRestartExperimentReusesGuestAndChecksEveryCycle(t *testing.T) {
	for _, value := range []string{"", "1", "2", "3"} {
		t.Run("cycles="+value, func(t *testing.T) {
			out, trace, err := runBootRestartExperimentShell(t, value, "", 0, bootRestartExperimentInvocation)
			if err != nil {
				t.Fatalf("experiment failed: %v\n%s\n%s", err, out, trace)
			}
			cycles, _ := strconv.Atoi(value)
			if cycles == 0 {
				cycles = 1
			}
			for _, boundary := range []string{"restart", "poll", "attempt-exact", "authority", "privacy", "foreign"} {
				if got := strings.Count(trace, boundary+"\n"); got != cycles {
					t.Fatalf("%s called %d times, want %d: %s", boundary, got, cycles, trace)
				}
			}
			wantTraffic := 0
			if cycles > 1 {
				wantTraffic = cycles
			}
			if got := strings.Count(trace, "traffic\n"); got != wantTraffic {
				t.Fatalf("diagnostic traffic checks=%d, want %d", got, wantTraffic)
			}
			if got := strings.Count(trace, "pass:"); got != 5 {
				t.Fatalf("expected five aggregate restart predicates exactly once: %s", trace)
			}
			lastCheck := strings.LastIndex(trace, "foreign\n")
			if cycles > 1 {
				lastCheck = strings.LastIndex(trace, "traffic\n")
			}
			if strings.Index(trace, "pass:") < lastCheck || strings.Contains(trace, "diagnostic\n") {
				t.Fatalf("premature PASS or unnecessary failure probe: %s", trace)
			}
			if cycles > 1 && !strings.Contains(out, "boot-restart-experiment.outcome=not-reproduced") {
				t.Fatalf("successful diagnostic must not claim a product fix: %s", out)
			}
		})
	}
}

func TestHostedVMBootAuthorityRestartExperimentStopsAtFirstFailedPoll(t *testing.T) {
	for cycle := 1; cycle <= 3; cycle++ {
		t.Run(fmt.Sprintf("cycle-%d", cycle), func(t *testing.T) {
			out, trace, err := runBootRestartExperimentShell(t, "3", "poll", cycle, bootRestartExperimentInvocation)
			if err == nil || strings.Count(trace, "restart\n") != cycle || strings.Count(trace, "diagnostic\n") != 1 {
				t.Fatalf("first failure must stop without another restart: err=%v\n%s\n%s", err, out, trace)
			}
			if strings.Contains(trace, "pass:") || strings.Contains(out, "outcome=not-reproduced") {
				t.Fatalf("a failed cycle was masked: %s\n%s", out, trace)
			}
			if !strings.Contains(out, "dns_scope_cause=command-timeout") ||
				!strings.Contains(out, "failure.step=daemon.same_boot_restart.resume.connect-replay.failed.resolved-link-query-verify.none.completed.incomplete") {
				t.Fatalf("first failure evidence lost: %s", out)
			}
		})
	}
}

func TestHostedVMBootAuthorityRestartExperimentFailsClosedAtGuestBoundaries(t *testing.T) {
	for _, fault := range []string{"restart", "pid-read", "pid-unchanged", "attempt-read", "attempt-mismatch", "attempt-exact", "session-read", "session-mismatch", "authority", "privacy", "foreign", "authority-intermediate", "boot-read", "boot-read-with-output", "boot-mismatch", "traffic"} {
		t.Run(fault, func(t *testing.T) {
			out, trace, err := runBootRestartExperimentShell(t, "3", fault, 2, bootRestartExperimentInvocation)
			if err == nil || strings.Count(trace, "restart\n") != 2 || strings.Contains(trace, "pass:") {
				t.Fatalf("failed %s was swallowed: err=%v\n%s\n%s", fault, err, out, trace)
			}
			if strings.Contains(out, "outcome=not-reproduced") || strings.Contains(out, "profile-a") || strings.Contains(out, "session-a") {
				t.Fatalf("false success or private identity in public output: %s", out)
			}
		})
	}
}

func TestHostedVMBootAuthorityRestartExperimentRejectsBudgetBeforeMutation(t *testing.T) {
	_, trace, err := runBootRestartExperimentShell(t, "4", "", 0, bootRestartExperimentInvocation)
	if err == nil || trace != "" {
		t.Fatalf("invalid budget reached guest operations: err=%v trace=%q", err, trace)
	}
	text := bootRestartExperimentSource(t)
	mainBody := bootRestartExperimentFunction(t, text, "main")
	if validation, init := strings.Index(mainBody, "boot_restart_cycles"), strings.Index(mainBody, "hosted_vm_init"); validation < 0 || init < 0 || validation > init {
		t.Fatal("main must reject the budget before guest provisioning")
	}
	if !strings.Contains(bootRestartExperimentFunction(t, text, "run_scenario"), "run_active_daemon_restarts") {
		t.Fatal("real scenario does not invoke the tested restart sequence")
	}
}

func TestHostedVMBootAuthorityRestartExperimentWorkflowHasSingleBudgetedJob(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/hosted-vm-boot-authority.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, marker := range []string{"restart_cycles:", "type: choice", "default: '1'", "options: ['1', '2', '3']", "PODLAZ_E2E_BOOT_RESTART_CYCLES:", "inputs.restart_cycles || '1'", "timeout-minutes: 30"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("manual diagnostic budget missing %q", marker)
		}
	}
	if strings.Contains(text, "matrix:") || strings.Count(text, "run: bash scripts/build-deb.sh") != 1 {
		t.Fatal("diagnostic must not multiply jobs or package builds")
	}
	contract := strings.Index(text, "- name: Scenario contract")
	tools := strings.Index(text, "- name: Install VM qualification tools")
	if contract < 0 || tools < 0 || contract > tools {
		t.Fatal("cheap scenario contracts must precede expensive provisioning")
	}
	validation := strings.Index(text, "- name: Validate diagnostic budget")
	if validation < 0 || validation > strings.Index(text, "- name: Setup Go") {
		t.Fatal("invalid dispatch inputs must fail before setup/build/guest cost")
	}
}

const bootRestartExperimentInvocation = `
# Execute like the real script: unguarded calls under errexit. Shared guest
# helpers can contain multiple commands and must not inherit a conditional.
trap 'rc=$?; trap - EXIT; printf "failure.step=%s\n" "${FAILURE_STEP:-none}"; exit "$rc"' EXIT
run_active_daemon_restarts boot-a generation-a profile-a attempt-a session-a
`

func runBootRestartExperimentShell(t *testing.T, cycles, fault string, failureCycle int, invocation string) (string, string, error) {
	t.Helper()
	text := bootRestartExperimentSource(t)
	var functions strings.Builder
	for _, name := range []string{"mark_failure", "boot_restart_cycles", "restart_active_daemon", "run_active_daemon_restarts"} {
		functions.WriteString(bootRestartExperimentFunction(t, text, name))
		functions.WriteByte('\n')
	}
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", "set -Eeuo pipefail\n"+functions.String()+bootRestartGuestFixture+invocation)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PODLAZ_E2E_BOOT_RESTART_CYCLES=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "TRACE="+tracePath, "FAULT="+fault, fmt.Sprintf("FAULT_CYCLE=%d", failureCycle))
	if cycles != "" {
		cmd.Env = append(cmd.Env, "PODLAZ_E2E_BOOT_RESTART_CYCLES="+cycles)
	}
	out, err := cmd.CombinedOutput()
	trace, readErr := os.ReadFile(tracePath)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	return string(out), string(trace), err
}

func bootRestartExperimentSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("hosted-vm-boot-authority.sh")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func bootRestartExperimentFunction(t *testing.T, text, name string) string {
	t.Helper()
	start := strings.Index(text, "\n"+name+"() {")
	if start < 0 {
		t.Fatalf("missing scenario helper %s", name)
	}
	start++
	end := strings.Index(text[start:], "\n}\n")
	if end < 0 {
		t.Fatalf("unterminated scenario helper %s", name)
	}
	return text[start : start+end+3]
}

// Only external guest operations are substituted. The actual scenario's
// restart sequencing, failure propagation and evidence publication execute.
// This fixture proves harness behavior, not real DNS or hosted acceptance.
const bootRestartGuestFixture = `
CYCLE=0
boundary() {
  printf '%s\n' "$1" >>"$TRACE"
  ! [[ "$FAULT" == "$1" && "$CYCLE" == "$FAULT_CYCLE" ]]
}
fault_is() { [[ "$FAULT" == "$1" && "$CYCLE" == "$FAULT_CYCLE" ]]; }
hosted_vm_ga_bash() {
  case "$1" in
    'systemctl restart podlazd.service')
      CYCLE=$((CYCLE + 1))
      boundary restart ;;
    'systemctl show -p MainPID --value podlazd.service')
      boundary pid-read || return 1
      if fault_is pid-unchanged; then printf '%s\n' "$((100 + CYCLE - 1))"; else printf '%s\n' "$((100 + CYCLE))"; fi ;;
    *) printf 'unexpected guest operation\n' >&2; return 1 ;;
  esac
}
hosted_vm_tun_wait_status() {
  [[ "$*" == 'verified-active 180 diagnose' ]] || return 1
  boundary poll
}
attempt_sha() {
  boundary attempt-read || return 1
  if fault_is attempt-mismatch; then printf 'attempt-b\n'; else printf 'attempt-a\n'; fi
}
assert_attempt_exact() {
  [[ "$*" == 'boot-a generation-a profile-a succeeded' ]] || return 1
  boundary attempt-exact
}
hosted_vm_tun_session_id() {
  boundary session-read || return 1
  if fault_is session-mismatch; then printf 'session-b\n'; else printf 'session-a\n'; fi
}
hosted_vm_boot_id() {
  if fault_is boot-read-with-output; then printf 'boot-a\n'; return 1; fi
  boundary boot-read || return 1
  if fault_is boot-mismatch; then printf 'boot-b\n'; else printf 'boot-a\n'; fi
}
hosted_vm_tun_capture_exact_active_authority() {
  [[ "$*" == no ]]
  boundary authority
  if fault_is authority-intermediate; then false; fi
  printf 'authority-tail\n' >>"$TRACE"
}
hosted_vm_tun_assert_direct_blocked() { boundary privacy; }
hosted_vm_tun_assert_foreign_state() { boundary foreign; }
hosted_vm_tun_run_traffic() { boundary traffic; }
boot_restart_readonly_diagnostic() {
  boundary diagnostic || return 1
  printf 'boot-restart-diagnostic.dns_scope_cause=command-timeout\n'
}
daemon_restart_failure_token() {
  printf 'resume.connect-replay.failed.resolved-link-query-verify.none.completed.incomplete\n'
}
record_evidence() { printf 'pass:%s=%s\n' "$1" "$2" >>"$TRACE"; }
`
