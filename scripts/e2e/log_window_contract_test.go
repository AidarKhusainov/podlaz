package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestStrictLogWindowAcceptanceProvesVisibleOldAndFreshMarkers(t *testing.T) {
	data, err := os.ReadFile("log-window-acceptance.sh")
	if err != nil {
		t.Fatalf("read strict log-window acceptance: %v", err)
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
			t.Fatalf("strict log-window acceptance lost %q", required)
		}
	}

	broadMarker := strings.Index(script, "broad_window_marker_visible")
	shortWindow := strings.Index(script, "logs --daemon --since 5s")
	if broadMarker < 0 || shortWindow < 0 || broadMarker >= shortWindow {
		t.Fatal("broad CLI-visible marker proof must precede the short-window exclusion check")
	}
}
