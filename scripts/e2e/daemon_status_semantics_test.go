package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestDaemonStatusSemanticsAcceptsTerminalInactiveWithoutAuthority(t *testing.T) {
	statusPath := writeDaemonStatusFixture(t, map[string]any{
		"connection":            "inactive",
		"active_transaction_id": "",
		"terminal_reason":       "vpn_restore_failed",
		"transactions":          []map[string]any{},
	})

	runDaemonStatusPredicate(t, "terminal-inactive", statusPath, true)

	cleanupRequiredPath := writeDaemonStatusFixture(t, map[string]any{
		"connection":            "inactive",
		"active_transaction_id": "",
		"terminal_reason":       "vpn_restore_failed",
		"transactions": []map[string]any{
			{
				"id":               "tx-example",
				"state":            "failed",
				"requires_cleanup": true,
			},
		},
	})
	runDaemonStatusPredicate(t, "terminal-inactive", cleanupRequiredPath, false)

	wrongReasonPath := writeDaemonStatusFixture(t, map[string]any{
		"connection":            "inactive",
		"active_transaction_id": "",
		"terminal_reason":       "vpn_connect_failed",
		"transactions":          []map[string]any{},
	})
	runDaemonStatusPredicate(t, "terminal-inactive", wrongReasonPath, false)
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

func TestDaemonStatusSemanticsDiagnosesMissingActiveTransactionIdentity(t *testing.T) {
	statusPath := writeDaemonStatusFixture(t, map[string]any{
		"connection": "active",
		"mode":       "tun",
		"tun_health": map[string]any{"state": "verified"},
		"transactions": []map[string]any{
			{
				"id":               "tx-example",
				"state":            "committed",
				"requires_cleanup": false,
			},
		},
	})

	if got := runDaemonStatusDiagnosis(t, statusPath); got != "active-missing-transaction-id" {
		t.Fatalf("diagnosis = %q, want active-missing-transaction-id", got)
	}
}

func TestDaemonStatusSemanticsPrefersTypedResumeDiagnostic(t *testing.T) {
	statusPath := writeDaemonStatusFixture(t, map[string]any{
		"connection": "active",
		"mode":       "tun",
		"tun_health": map[string]any{"state": "revalidating"},
		"transactions": []map[string]any{
			{
				"id":               "tx-example",
				"state":            "committed",
				"requires_cleanup": false,
			},
		},
	})
	diagnosticPath := writeDaemonStatusFixture(t, map[string]any{
		"resume_stage":           "connect-replay",
		"last_resume_outcome":    "failed",
		"tun_failure_phase":      "network-apply",
		"network_apply_subphase": "tun-address",
		"rollback_status":        "completed",
		"replay_disposition":     "retryable",
	})

	if got := runDaemonStatusDiagnosis(t, statusPath, diagnosticPath); got != "resume.connect-replay.failed.network-apply.tun-address.completed.retryable" {
		t.Fatalf("diagnosis = %q, want typed resume diagnostic with apply subphase", got)
	}
}

func TestDaemonStatusSemanticsDiagnosesRebuildFailureFromTunDiagnostic(t *testing.T) {
	statusPath := writeDaemonStatusFixture(t, map[string]any{
		"connection": "active",
		"mode":       "tun",
		"tun_health": map[string]any{
			"state":          "degraded",
			"classification": "owned_state_invalid",
		},
		"transactions": []map[string]any{
			{
				"id":               "tx-example",
				"state":            "committed",
				"requires_cleanup": false,
			},
		},
	})
	diagnosticPath := writeDaemonStatusFixture(t, map[string]any{
		"failure_phase":          "network-apply",
		"rollback_status":        "completed",
		"primary_classification": "tun_address_apply_failure",
		"session": map[string]any{
			"state":        "verifying",
			"core_running": true,
		},
	})

	args := []string{
		filepath.Join("lib", "daemon_status_semantics.py"),
		"diagnose-rebuild",
		statusPath,
		diagnosticPath,
	}
	cmd := exec.Command("python3", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("daemon rebuild diagnosis failed: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	want := "rebuild.active.degraded.owned_state_invalid.network-apply.completed.tun_address_apply_failure.verifying.core-running"
	if got != want {
		t.Fatalf("rebuild diagnosis = %q, want %q", got, want)
	}
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

func runDaemonStatusDiagnosis(t *testing.T, statusPath string, diagnosticPath ...string) string {
	t.Helper()
	args := []string{filepath.Join("lib", "daemon_status_semantics.py"), "diagnose-active", statusPath}
	args = append(args, diagnosticPath...)
	cmd := exec.Command("python3", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("daemon status diagnosis failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}
