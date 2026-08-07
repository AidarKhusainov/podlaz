package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestXrayManagerStatusSeparatesRuntimeAndTransactionInspectionWarnings(t *testing.T) {
	runtimeDir := t.TempDir()
	transactionDir := filepath.Join(runtimeDir, txstate.TransactionDirName)
	if err := os.MkdirAll(transactionDir, 0o700); err != nil {
		t.Fatalf("create transaction directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(transactionDir, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write malformed transaction fixture: %v", err)
	}

	manager := NewXrayManager(runtimeDir)
	manager.state = xrayState{
		Connection: "active",
		Mode:       "tun",
		Proxy:      "active",
		TUN:        "active",
		Warnings:   []string{"runtime warning fixture"},
	}

	status := manager.Status(context.Background())
	if len(status.Warnings) != 1 || status.Warnings[0] != "runtime warning fixture" {
		t.Fatalf("runtime warnings were polluted by inspection failures: %#v", status.Warnings)
	}
	if len(status.InspectionWarnings) != 1 {
		t.Fatalf("expected one transaction inspection warning, got %#v", status.InspectionWarnings)
	}
	warning := status.InspectionWarnings[0]
	if warning.Target != "transaction state" || !strings.Contains(warning.Message, "broken.json") {
		t.Fatalf("unexpected transaction inspection warning: %#v", warning)
	}
}
