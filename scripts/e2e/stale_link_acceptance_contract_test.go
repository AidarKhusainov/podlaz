package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestStaleLinkPackageAcceptanceCoversRequiredLifecycleAndLogsCases(t *testing.T) {
	data, err := os.ReadFile("stale-link-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read stale-link acceptance: %v", err)
	}
	script := string(data)

	for _, required := range []string{
		"assert_active_nft_mismatch_warns",
		"nft add chain inet podlaz",
		"nft table inet podlaz does not match active transaction exact composition",
		"assert_logs_since_mode_36h daemon 'podlaz daemon logs' daemon",
		"assert_logs_since_mode_36h core 'podlaz core logs' core",
		"assert_logs_lookback_is_bounded",
		"logs --daemon --since 1s",
		"assert_logs_follow_cancels_cleanly",
		"--signal=INT",
		"finish_exit_trap",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("stale-link acceptance lost required contract %q", required)
		}
	}

	mismatch := strings.LastIndex(script, "assert_active_nft_mismatch_warns")
	disconnect := strings.Index(script[mismatch:], "run_installed_podlaz_bounded 60s disconnect")
	if mismatch < 0 || disconnect < 0 {
		t.Fatal("stale-link acceptance must exercise active mismatch before disconnect")
	}
}

func TestStaleLinkAcceptanceWritesPassOnlyFromCleanup(t *testing.T) {
	data, err := os.ReadFile("stale-link-package-acceptance.sh")
	if err != nil {
		t.Fatalf("read stale-link acceptance: %v", err)
	}
	script := string(data)
	if count := strings.Count(script, "write_evidence acceptance pass"); count != 1 {
		t.Fatalf("acceptance pass must have one guarded write, got %d", count)
	}
	cleanupStart := strings.Index(script, "cleanup() {")
	pass := strings.Index(script, "write_evidence acceptance pass")
	finish := strings.Index(script, "finish_exit_trap")
	if cleanupStart < 0 || pass < cleanupStart || finish < pass {
		t.Fatal("acceptance pass must be written inside successful cleanup before explicit final exit")
	}
}
