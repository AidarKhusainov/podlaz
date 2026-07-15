package tundiag

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreAtomicallyReplacesOnePrivateLatestReport(t *testing.T) {
	runtimeDir := t.TempDir()
	store := Store{RuntimeDir: runtimeDir, Now: func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) }}
	first := Report{Session: Session{State: "active"}, Probes: []ProbeResult{{ID: "route", Status: ProbePass}}}
	path, err := store.Save(first)
	if err != nil {
		t.Fatal(err)
	}
	second := Report{Session: Session{State: "active"}, Probes: []ProbeResult{{ID: "dns", Status: ProbeFail, Classification: ClassDNSUDPFailure}}}
	if _, err := store.Save(second); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != LastReportFileName {
		t.Fatalf("expected replacement-only latest report, got %#v", entries)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected private report mode 0600, got %o", got)
	}
	loaded, loadedPath, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loadedPath != path || !loaded.Historical || loaded.PrimaryClassification != ClassDNSUDPFailure {
		t.Fatalf("unexpected loaded report: %#v path=%s", loaded, loadedPath)
	}
}

func TestStoreRejectsUnboundedReport(t *testing.T) {
	store := Store{RuntimeDir: t.TempDir()}
	probes := make([]ProbeResult, 2000)
	for i := range probes {
		probes[i] = ProbeResult{ID: "oversized-probe", Status: ProbeFail, Classification: ClassInternalDiagnosticError, Error: string(make([]byte, 4096))}
	}
	_, err := store.Save(Report{Probes: probes})
	if err == nil {
		t.Fatal("expected size limit failure")
	}
}
