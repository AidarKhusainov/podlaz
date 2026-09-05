package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDaemonStatusSemanticsAcceptsProductionShapedVerifiedTun(t *testing.T) {
	statusPath := writeDaemonStatusFixture(t, map[string]any{
		"connection":            "active",
		"mode":                  "tun",
		"tun":                   "enabled (podlaz0)",
		"active_transaction_id": "tx-example",
		"tun_health": map[string]any{
			"state": "verified",
		},
		"transactions": []map[string]any{
			{
				"id":               "tx-example",
				"state":            "committed",
				"requires_cleanup": false,
			},
		},
	})

	runDaemonStatusPredicate(t, "verified-active", statusPath, true)
}

func TestDaemonStatusSemanticsIgnoresTunPresentationForCleanInactive(t *testing.T) {
	statusPath := writeDaemonStatusFixture(t, map[string]any{
		"connection":            "inactive",
		"tun":                   "presentation-only value",
		"active_transaction_id": "",
		"transactions":          []map[string]any{},
	})

	runDaemonStatusPredicate(t, "clean-inactive", statusPath, true)
}

func TestDaemonStatusSemanticsRequiresExactCommittedActiveTransaction(t *testing.T) {
	statusPath := writeDaemonStatusFixture(t, map[string]any{
		"connection":            "active",
		"mode":                  "tun",
		"tun":                   "enabled (podlaz0)",
		"active_transaction_id": "tx-expected",
		"tun_health": map[string]any{
			"state": "verified",
		},
		"transactions": []map[string]any{
			{
				"id":               "tx-other",
				"state":            "committed",
				"requires_cleanup": false,
			},
		},
	})

	runDaemonStatusPredicate(t, "verified-active", statusPath, false)
}

func TestDaemonStatusSemanticsFailsClosedOnCleanupOrUnverifiedHealth(t *testing.T) {
	cleanupPath := writeDaemonStatusFixture(t, map[string]any{
		"connection":            "active",
		"mode":                  "tun",
		"tun":                   "enabled (podlaz0)",
		"active_transaction_id": "tx-example",
		"tun_health": map[string]any{
			"state": "verified",
		},
		"transactions": []map[string]any{
			{
				"id":               "tx-example",
				"state":            "committed",
				"requires_cleanup": true,
			},
		},
	})
	runDaemonStatusPredicate(t, "verified-active", cleanupPath, false)

	revalidatingPath := writeDaemonStatusFixture(t, map[string]any{
		"connection":            "active",
		"mode":                  "tun",
		"tun":                   "enabled (podlaz0)",
		"active_transaction_id": "tx-example",
		"tun_health": map[string]any{
			"state": "revalidating",
		},
		"transactions": []map[string]any{
			{
				"id":               "tx-example",
				"state":            "committed",
				"requires_cleanup": false,
			},
		},
	})
	runDaemonStatusPredicate(t, "verified-active", revalidatingPath, false)
}

func writeDaemonStatusFixture(t *testing.T, payload map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "status.json")
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal daemon status fixture: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write daemon status fixture: %v", err)
	}
	return path
}

func runDaemonStatusPredicate(t *testing.T, target, statusPath string, wantMatch bool) {
	t.Helper()
	cmd := exec.Command("python3", filepath.Join("lib", "daemon_status_semantics.py"), target, statusPath)
	err := cmd.Run()
	if wantMatch && err != nil {
		t.Fatalf("daemon status predicate %q rejected fixture: %v", target, err)
	}
	if !wantMatch && err == nil {
		t.Fatalf("daemon status predicate %q unexpectedly accepted fixture", target)
	}
}
