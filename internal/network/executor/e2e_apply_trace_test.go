package executor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestRecordE2EApplyTraceRequiresArmedRollbackPause(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PODLAZ_E2E_TUN_ROLLBACK_PAUSE", "true")
	t.Setenv("PODLAZ_E2E_TUN_ROLLBACK_PAUSE_DIR", dir)

	recordE2EApplyTrace("tun-preapply-started")
	path := filepath.Join(dir, "apply-trace.tun-preapply-started")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unarmed trace must not create marker: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "rollback-pause.arm"), []byte("armed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recordE2EApplyTrace("tun-preapply-started")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("armed trace marker missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("trace marker mode=%o want=600", info.Mode().Perm())
	}
}

func TestRecordE2EApplyTraceRejectsUnknownEvent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PODLAZ_E2E_TUN_ROLLBACK_PAUSE", "true")
	t.Setenv("PODLAZ_E2E_TUN_ROLLBACK_PAUSE_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "rollback-pause.arm"), []byte("armed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	recordE2EApplyTrace("../escape")
	if _, err := os.Stat(filepath.Join(dir, "escape")); !os.IsNotExist(err) {
		t.Fatalf("unknown trace event escaped marker directory: %v", err)
	}
}

func TestDNSAwareApplyTraceAttributesValidationFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PODLAZ_E2E_TUN_ROLLBACK_PAUSE", "true")
	t.Setenv("PODLAZ_E2E_TUN_ROLLBACK_PAUSE_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "rollback-pause.arm"), []byte("armed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	exec := DNSAwareTunExecutor{}
	if _, err := exec.ApplyWithStepSink(context.Background(), planner.TunPlan{}, nil); err == nil {
		t.Fatal("expected DNS-aware validation failure")
	}
	for _, event := range []string{"dnsaware-entered", "dnsaware-validate-dns-failed"} {
		if _, err := os.Stat(filepath.Join(dir, "apply-trace."+event)); err != nil {
			t.Fatalf("missing validation trace %s: %v", event, err)
		}
	}
}
