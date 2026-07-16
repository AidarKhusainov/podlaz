package tundiag

import (
	"errors"
	"os"
	"testing"
)

func TestStoreFailedReplacementInvalidatesPreviousReport(t *testing.T) {
	store := Store{RuntimeDir: t.TempDir()}
	if _, err := store.Save(Report{
		Session: Session{State: "inactive"},
		Probes:  []ProbeResult{{ID: "previous", Status: ProbePass}},
	}); err != nil {
		t.Fatalf("save previous report: %v", err)
	}

	probes := make([]ProbeResult, 2000)
	for i := range probes {
		probes[i] = ProbeResult{
			ID:             "oversized-probe",
			Status:         ProbeFail,
			Classification: ClassInternalDiagnosticError,
			Error:          string(make([]byte, 4096)),
		}
	}
	if path, err := store.Save(Report{Probes: probes}); err == nil || path != "" {
		t.Fatalf("expected failed replacement with empty path, got path=%q err=%v", path, err)
	}

	if _, _, err := store.Load(); err == nil || !os.IsNotExist(err) {
		t.Fatalf("failed replacement left a loadable previous report: %v", err)
	}
}

func TestStoreDirectorySyncFailureInvalidatesRenamedReport(t *testing.T) {
	runtimeDir := t.TempDir()
	store := Store{RuntimeDir: runtimeDir}
	if _, err := store.Save(Report{Probes: []ProbeResult{{ID: "previous", Status: ProbePass}}}); err != nil {
		t.Fatalf("save previous report: %v", err)
	}

	failingStore := Store{
		RuntimeDir: runtimeDir,
		syncDirectory: func(string) error {
			return errors.New("injected directory sync failure")
		},
	}
	if path, err := failingStore.Save(Report{Probes: []ProbeResult{{ID: "replacement", Status: ProbePass}}}); err == nil || path != "" {
		t.Fatalf("expected directory sync failure with empty path, got path=%q err=%v", path, err)
	}
	if _, _, err := store.Load(); err == nil || !os.IsNotExist(err) {
		t.Fatalf("directory sync failure left a loadable report: %v", err)
	}
}
