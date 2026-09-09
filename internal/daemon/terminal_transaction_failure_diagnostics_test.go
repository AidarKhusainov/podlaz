package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/doctor"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestTransactionDoctorCheckPreservesDurableTerminalFailure(t *testing.T) {
	runtimeDir := t.TempDir()
	now := time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)
	tx := txstate.NewTransaction("tun-terminal-diagnostic", "profile-example", "tun", now)
	tx.State = txstate.TransactionFailed
	tx.FailureReason = "synthetic exact firewall rollback blocker"
	if _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Save(tx); err != nil {
		t.Fatal(err)
	}

	check := transactionDoctorCheck(runtimeDir, tx.ID)
	if check.Severity != doctor.SeverityWarning {
		t.Fatalf("failed cleanup-required transaction severity=%q, want WARN", check.Severity)
	}
	if !strings.Contains(check.Message, "durable failure") || !strings.Contains(check.Message, "synthetic exact firewall rollback blocker") {
		t.Fatalf("doctor transaction check masked durable terminal blocker: %q", check.Message)
	}
}
