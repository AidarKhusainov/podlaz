package tundiag

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestStorePersistsLifecycleFailureAndRollbackStatus(t *testing.T) {
	store := Store{RuntimeDir: t.TempDir(), Now: func() time.Time {
		return time.Date(2026, time.July, 21, 8, 0, 0, 0, time.UTC)
	}}
	report := Report{
		FailurePhase:   "network-verify",
		RollbackStatus: "pending",
		Session: Session{
			State:         "verifying",
			Mode:          "tun",
			TransactionID: "tun-example",
		},
		Probes: []ProbeResult{{
			ID:             "dns-link",
			Layer:          LayerDNS,
			Status:         ProbeFail,
			Classification: ClassDNSApplyFailure,
		}},
	}

	path, err := store.Save(report)
	if err != nil {
		t.Fatalf("save lifecycle report: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat lifecycle report: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("unexpected lifecycle report mode: %o", got)
	}

	loaded, loadedPath, err := store.Load()
	if err != nil {
		t.Fatalf("load lifecycle report: %v", err)
	}
	if loadedPath != path || loaded.ReportPath != path {
		t.Fatalf("unexpected report path: loaded=%q model=%q want=%q", loadedPath, loaded.ReportPath, path)
	}
	if loaded.SchemaVersion != SchemaVersion || !loaded.Historical {
		t.Fatalf("expected a versioned historical report, got %#v", loaded)
	}
	if loaded.FailurePhase != "network-verify" || loaded.RollbackStatus != "pending" {
		t.Fatalf("unexpected lifecycle metadata: %#v", loaded)
	}

	loaded.RollbackStatus = "completed"
	if _, err := store.Save(loaded); err != nil {
		t.Fatalf("finalize rollback status: %v", err)
	}
	final, _, err := store.Load()
	if err != nil {
		t.Fatalf("reload finalized report: %v", err)
	}
	if final.RollbackStatus != "completed" || final.FailurePhase != "network-verify" {
		t.Fatalf("unexpected finalized lifecycle metadata: %#v", final)
	}
	human := RenderHuman(final, true)
	for _, want := range []string{"Failure phase network-verify", "Rollback      completed", "saved result"} {
		if !strings.Contains(human, want) {
			t.Fatalf("human report missing %q:\n%s", want, human)
		}
	}
}
