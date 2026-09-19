package e2e_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const (
	hostedFailClosedWorkflow    = "../../.github/workflows/hosted-recovery.yml"
	hostedPrecommitInterruption = "hosted-precommit-interruption.sh"
)

func readHostedFailClosedFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireHostedFailClosedMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted fail-closed contract lost %q", marker)
		}
	}
}

func forbidHostedFailClosedMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			t.Fatalf("hosted fail-closed contract contains forbidden %q", marker)
		}
	}
}

func TestHostedCurrentRuntimeFailClosedOwnershipIsPermanentRecoveryJob(t *testing.T) {
	workflow := readHostedFailClosedFile(t, hostedFailClosedWorkflow)
	requireHostedFailClosedMarkers(t, workflow,
		"precommit-interruption:",
		"name: Pre-commit interruption ownership",
		"go test ./scripts/e2e -run '^TestHostedCurrentRuntimeFailClosedOwnership' -count=1",
		"bash scripts/e2e/hosted-precommit-interruption.sh",
		"podlaz-hosted-precommit-interruption",
		"hosted-precommit-interruption.txt",
	)
	forbidHostedFailClosedMarkers(t, workflow,
		"orphan-routing-preflight:",
		"self-hosted",
		"${{ secrets.",
		"PODLAZ_E2E_PROFILE_URI",
	)
}

func TestHostedCurrentRuntimeFailClosedOwnershipCannotPublishOrFabricateAuthority(t *testing.T) {
	script := readHostedFailClosedFile(t, hostedPrecommitInterruption)
	requireHostedFailClosedMarkers(t, script,
		`BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		"PODLAZ_E2E_TUN_HOOKS=true",
		"PODLAZ_E2E_TUN_HOOK_PHASE=before-commit-pause",
		"before-commit-pause.ready",
		"systemctl kill",
		"SIGKILL",
		"assert_precommit_transaction_only",
		"capture_precommit_session_identity",
		"podlaz.network-session-state.v1",
		"pre-commit Network Session fabricated Privacy Envelope authority",
		"assert_no_published_or_resumed_authority",
		"recover --json",
		"recover --execute --yes --json",
		"assert_network_snapshot_equal",
		"assert_no_hidden_reconnect",
		"connect.interrupted_not_success",
		"restart.no_false_resume_authority",
		"recovery.exact_transaction_only",
		"recovery.insufficient_authority_preserved",
		`"resume_stage": "exact-recovery"`,
		`"cleanup_authority": "none"`,
		`"next_action": "retry-resume"`,
		"pre-commit dry-run advanced into connect replay",
		"recover completed with incomplete cleanup",
		"foreign.state_preserved",
		"direct.connectivity_preserved",
		"artifact.privacy",
	)
	forbidHostedFailClosedMarkers(t, script,
		"ip link del podlaz0",
		"ip route flush",
		"ip rule flush",
		"nft flush",
		"rm -rf /run/podlaz",
		"table 51820",
		"priority 9999",
		"priority 10000",
	)
	cmd := exec.Command("bash", "-n", hostedPrecommitInterruption)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedPrecommitInterruption, err, output)
	}
}
