package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostedVMBootAuthorityRestartPollingUsesInstalledVerifierAndBoundedAttempt(t *testing.T) {
	script, err := os.ReadFile("hosted-vm-boot-authority.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, token := range []string{
		"hosted_vm_tun_wait_status verified-active 180 diagnose",
		"boot_restart_readonly_diagnostic",
		"/var/tmp/podlaz-hosted-vm-daemon-status-semantics.py",
		"/var/tmp/podlaz-hosted-vm-tun/restart-status-diagnostic.json",
		"boot-restart-diagnostic.active_state=",
		"boot-restart-diagnostic.main_pid=",
		"boot-restart-diagnostic.result=",
		"boot-restart-diagnostic.sub_state=",
		"boot-restart-diagnostic.restart_count=",
		"boot-restart-diagnostic.startup_phase=",
		"boot-restart-diagnostic.startup_phase_age_seconds=",
		"/run/podlaz/startup-progress",
		"boot-restart-timing.systemctl_seconds=",
	} {
		if !strings.Contains(text, token) {
			t.Fatalf("boot restart lost %q", token)
		}
	}
	for _, forbidden := range []string{
		"python3 /tmp/daemon_status_semantics.py",
		"status=/tmp/podlaz-hosted-vm-tun/",
		">/tmp/podlaz-hosted-vm-tun/",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("invalid diagnostic helper path: %q", forbidden)
		}
	}
	shared, err := os.ReadFile("lib/hosted_vm_tun.sh")
	if err != nil {
		t.Fatal(err)
	}
	first := strings.Index(string(shared), "hosted_vm_tun_wait_status_diagnosed() {")
	last := strings.Index(string(shared), "\nhosted_vm_tun_wait_status() {")
	if first < 0 || last <= first {
		t.Fatal("classified wait must be isolated from unchanged generic helper")
	}
	poll := string(shared[first:last])
	for _, expected := range []string{
		"attempt <= attempts", "sleep 1", "--max-time 5",
		"probe-verified-active", "guest-exec-failure", "transport-connect",
		"semantic-active-authority", "boot-restart-poll.attempts=",
		"boot-restart-poll.elapsed_seconds=", "boot-restart-poll.first_http_attempt=",
		"boot-restart-poll.last_class=", "boot-restart-poll.longest_streak_class=",
		"boot-restart-poll.longest_streak_attempts=", "boot-restart-poll.transitions=",
	} {
		if !strings.Contains(poll, expected) {
			t.Fatalf("poll lost %q", expected)
		}
	}
	for _, forbidden := range []string{
		"systemctl restart", "recover --execute", "journalctl", "ip link del", "nft delete", "profile-uri",
	} {
		if strings.Contains(poll, forbidden) {
			t.Fatalf("read-only diagnostic contains %q", forbidden)
		}
	}
}

func TestHostedVMBootAuthorityRestartPollingCountsFailedGuestProbesWithoutLeakingOutput(t *testing.T) {
	script := strings.Join([]string{
		"set -Eeuo pipefail",
		"HOSTED_VM_PROVIDER_ENDPOINT_IP=192.0.2.1",
		"source lib/hosted_vm_tun.sh",
		"sleep() { :; }",
		"hosted_vm_ga_bash() { printf 'transport-connect\\n'; }",
		"if hosted_vm_tun_wait_status verified-active 3 diagnose; then exit 5; fi",
	}, "\n")
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("classified poll command: %v: %s", err, out)
	}
	for _, token := range []string{
		"boot-restart-poll.outcome=fail",
		"boot-restart-poll.attempts=3",
		"boot-restart-poll.transport-connect=3",
		"boot-restart-poll.first_http_attempt=0",
		"boot-restart-poll.last_class=transport-connect",
		"boot-restart-poll.longest_streak_class=transport-connect",
		"boot-restart-poll.longest_streak_attempts=3",
		"boot-restart-poll.transitions=1",
	} {
		if !strings.Contains(string(out), token) {
			t.Fatalf("poll counter missing %q: %s", token, out)
		}
	}
}

func TestVerifiedActiveProbeUsesExactPredicateAndNoPrivateStatusFields(t *testing.T) {
	cases := []struct {
		name   string
		status map[string]any
		want   string
	}{
		{"match", map[string]any{
			"connection": "active", "mode": "tun", "active_transaction_id": "tx-example",
			"tun_health":   map[string]any{"state": "verified"},
			"transactions": []map[string]any{{"id": "tx-example", "state": "committed", "requires_cleanup": false}},
		}, "match"},
		{"unhealthy", map[string]any{"connection": "active", "mode": "tun", "tun_health": map[string]any{"state": "revalidating"}}, "semantic-active-health"},
		{"missing-authority", map[string]any{"connection": "active", "mode": "tun", "tun_health": map[string]any{"state": "verified"}}, "semantic-active-authority"},
		{"inactive", map[string]any{"connection": "inactive"}, "semantic-inactive"},
		{"redacted-terminal", map[string]any{"terminal_reason": "sensitive-user-identifier"}, "semantic-terminal"},
		{"cleanup", map[string]any{"transactions": []map[string]any{{"requires_cleanup": true}}}, "semantic-cleanup"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := writeDaemonStatusFixture(t, tc.status)
			args := []string{filepath.Join("lib", "daemon_status_semantics.py"), "probe-verified-active", file}
			out, err := exec.Command("python3", args...).CombinedOutput()
			if err != nil {
				t.Fatalf("probe: %v: %s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Fatalf("probe got %q, want %q", got, tc.want)
			}
			runDaemonStatusPredicate(t, "verified-active", file, tc.want == "match")
		})
	}
}
