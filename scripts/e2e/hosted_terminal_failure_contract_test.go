package e2e

import (
	"os"
	"strings"
	"testing"
)

const (
	hostedTerminalWorkflow  = "../../.github/workflows/hosted-recovery.yml"
	hostedTerminalScenario  = "hosted-terminal-failure-convergence.sh"
	hostedSyntheticScenario = "hosted-synthetic-tun.sh"
)

func TestHostedTerminalFailureConvergenceWorkflow(t *testing.T) {
	workflow := readHostedTerminalFile(t, hostedTerminalWorkflow)
	requireHostedTerminalMarkers(t, workflow,
		"terminal-failure-convergence:",
		"name: Terminal failure convergence",
		"go test ./scripts/e2e -run '^TestHostedTerminalFailureConvergence' -count=1",
		"bash scripts/e2e/hosted-terminal-failure-convergence.sh",
		"hosted-terminal-failure-convergence.txt",
		"podlaz-hosted-terminal-failure-convergence",
	)
}

func TestHostedTerminalFailureConvergenceScenario(t *testing.T) {
	script := readHostedTerminalFile(t, hostedTerminalScenario)
	requireHostedTerminalMarkers(t, script,
		`BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"`,
		"PODLAZ_E2E_HOSTED_EXPECT_EXTERNAL_TERMINAL=true",
		"PODLAZ_E2E_TUN_TERMINAL_FAILURE=true",
		"PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE=true",
		"terminal-failure.trigger",
		"terminal-data-plane-clean.ready",
		"terminal-data-plane-clean.continue",
		"privacy.envelope_retained",
		"privacy.direct_uplink_blocked",
		"foreign.state_preserved",
		"terminal.data_plane_clean",
		"terminal.converged_once",
		"terminal.authority_clean",
		"connectivity.restored",
		"terminal.no_hidden_retry",
		"artifact.privacy",
	)
}

func TestHostedSyntheticTunSupportsExternalTerminalConvergence(t *testing.T) {
	script := readHostedTerminalFile(t, hostedSyntheticScenario)
	requireHostedTerminalMarkers(t, script,
		"PODLAZ_E2E_HOSTED_EXPECT_EXTERNAL_TERMINAL",
		"terminal-inactive",
		"HOSTED_EXPECT_EXTERNAL_TERMINAL",
	)
}

func readHostedTerminalFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireHostedTerminalMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			t.Fatalf("missing %q", marker)
		}
	}
}
