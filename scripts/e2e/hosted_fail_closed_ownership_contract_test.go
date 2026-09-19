package e2e_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const (
	hostedFailClosedWorkflow    = "../../.github/workflows/hosted-recovery.yml"
	hostedOrphanPreflight       = "hosted-orphan-routing-preflight.sh"
	hostedPrecommitInterruption = "hosted-precommit-interruption.sh"
	hostedOrphanFixtureHelper   = "hosted_orphan_routing_fixture.py"
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

func TestHostedCurrentRuntimeFailClosedOwnershipKeepsFailureDomainsIndependent(t *testing.T) {
	workflow := readHostedFailClosedFile(t, hostedFailClosedWorkflow)
	requireHostedFailClosedMarkers(t, workflow,
		"orphan-routing-preflight:",
		"name: Orphan routing preflight ownership",
		"go test ./scripts/e2e -run '^TestHostedCurrentRuntimeFailClosedOwnership' -count=1",
		"bash scripts/e2e/hosted-orphan-routing-preflight.sh",
		"podlaz-hosted-orphan-routing-preflight",
		"hosted-orphan-routing-preflight.txt",
		"precommit-interruption:",
		"name: Pre-commit interruption ownership",
		"bash scripts/e2e/hosted-precommit-interruption.sh",
		"podlaz-hosted-precommit-interruption",
		"hosted-precommit-interruption.txt",
	)
	forbidHostedFailClosedMarkers(t, workflow,
		"self-hosted",
		"${{ secrets.",
		"PODLAZ_E2E_PROFILE_URI",
	)
}

func TestHostedOrphanRoutingPreflightUsesCurrentPersistedAuthorityAsForeignFixture(t *testing.T) {
	script := readHostedFailClosedFile(t, hostedOrphanPreflight)
	requireHostedFailClosedMarkers(t, script,
		`BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		"seed_orphan_routing_from_committed_generation",
		"hosted_synthetic_network_authority.py",
		"hosted_orphan_routing_fixture.py",
		"ambiguous stale routing state blocks TUN connect before network mutation",
		"ownership evidence is unavailable",
		"recover --json",
		"recover --execute --yes --json",
		"assert_orphan_fixture_unchanged",
		"assert_no_podlaz_authority_created",
		"assert_network_snapshot_equal",
		"preflight.blocked_before_mutation",
		"ownership.observation_not_authority",
		"recovery.unauthorized_noop",
		"foreign.state_preserved",
		"terminal.clean_after_fixture_removal",
		"artifact.privacy",
	)
	forbidHostedFailClosedMarkers(t, script,
		"table 51820",
		"priority 9999",
		"priority 10000",
		"198.18.0.1/32",
		"ip route flush",
		"ip rule flush",
		"nft flush",
		"rm -rf /run/podlaz",
	)
	cmd := exec.Command("bash", "-n", hostedOrphanPreflight)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedOrphanPreflight, err, output)
	}
}

func TestHostedOrphanRoutingFixtureMutatesOnlyExactTestOwnedManifest(t *testing.T) {
	helper := readHostedFailClosedFile(t, hostedOrphanFixtureHelper)
	requireHostedFailClosedMarkers(t, helper,
		"podlaz.e2e.hosted-network-authority.v1",
		"apply",
		"verify-present",
		"remove",
		"ip",
		"rules-only foreign fixture",
		"rule",
	)
	forbidHostedFailClosedMarkers(t, helper,
		"51820",
		"9999",
		"10000",
		"flush",
		"podlaz0",
	)
}

func TestHostedPrecommitInterruptionCannotPublishOrFabricateAuthority(t *testing.T) {
	script := readHostedFailClosedFile(t, hostedPrecommitInterruption)
	requireHostedFailClosedMarkers(t, script,
		`BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		"PODLAZ_E2E_TUN_HOOKS=true",
		"PODLAZ_E2E_TUN_HOOK_PHASE=before-commit-pause",
		"before-commit-pause.ready",
		"systemctl kill",
		"SIGKILL",
		"assert_precommit_transaction_only",
		"assert_no_published_or_resumed_authority",
		"recover --json",
		"recover --execute --yes --json",
		"assert_network_snapshot_equal",
		"assert_no_hidden_reconnect",
		"connect.interrupted_not_success",
		"restart.no_false_resume_authority",
		"recovery.exact_transaction_only",
		"foreign.state_preserved",
		"terminal.clean",
		"artifact.privacy",
	)
	forbidHostedFailClosedMarkers(t, script,
		"ip link del podlaz0",
		"ip route flush",
		"ip rule flush",
		"nft flush",
		"rm -rf /run/podlaz",
	)
	cmd := exec.Command("bash", "-n", hostedPrecommitInterruption)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedPrecommitInterruption, err, output)
	}
}
