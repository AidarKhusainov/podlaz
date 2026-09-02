package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestIssue247StrictLogWindowAcceptanceProvesVisibleOldAndFreshMarkers(t *testing.T) {
	data, err := os.ReadFile("issue247-log-window-acceptance.sh")
	if err != nil {
		t.Fatalf("read strict issue247 log-window acceptance: %v", err)
	}
	script := string(data)
	for _, required := range []string{
		"logs --daemon --since 30s",
		"broad_window_marker_visible",
		"sleep 8",
		"logs --daemon --since 5s",
		"short daemon log window did not contain the fresh visible marker",
		"journal line predates bounded lookback",
		"short_window_excludes_old_visible_marker",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("strict issue247 log-window acceptance lost %q", required)
		}
	}

	broadMarker := strings.Index(script, "broad_window_marker_visible")
	shortWindow := strings.Index(script, "logs --daemon --since 5s")
	if broadMarker < 0 || shortWindow < 0 || broadMarker >= shortWindow {
		t.Fatal("broad CLI-visible marker proof must precede the short-window exclusion check")
	}
}

func TestTunPackageConvergenceRunsStrictIssue247LogWindowAcceptance(t *testing.T) {
	data, err := os.ReadFile("../../.github/workflows/e2e-tun-package-convergence.yml")
	if err != nil {
		t.Fatalf("read TUN package convergence workflow: %v", err)
	}
	workflow := string(data)
	if !strings.Contains(workflow, "bash scripts/e2e/issue247-log-window-acceptance.sh") {
		t.Fatal("TUN package convergence must run the strict issue247 log-window acceptance")
	}
}
