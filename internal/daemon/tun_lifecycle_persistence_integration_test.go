package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestCollectTunFailureDiagnosticsPersistsStableLifecycleClassification(t *testing.T) {
	tests := []struct {
		phase string
		want  tundiag.Classification
	}{
		{phase: "network-apply", want: tundiag.ClassNetworkApplyFailure},
		{phase: "network-verify", want: tundiag.ClassNetworkVerifyFailure},
	}

	for _, tt := range tests {
		t.Run(tt.phase, func(t *testing.T) {
			runtimeDir := t.TempDir()
			manager := NewXrayManager(runtimeDir)
			manager.snapshotCollector = func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
				return netsnapshot.Snapshot{}
			}

			plan := transactionPlanForTest()
			transaction, err := beginTunTransaction(context.Background(), runtimeDir, profile.Profile{
				ID:   "example-profile",
				Name: "Example Profile",
			}, plan, fixedClock())
			if err != nil {
				t.Fatalf("begin TUN transaction: %v", err)
			}

			const secret = "vless://00000000-0000-0000-0000-000000000000@private.example.test:443"
			cause := &tunNetworkMutationError{
				phase: tt.phase,
				cause: errors.New("lifecycle failed for " + secret),
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			summary := manager.collectTunFailureDiagnostics(ctx, transaction.TransactionID, plan, cause)
			if !summary.Persisted || strings.TrimSpace(summary.ReportPath) == "" {
				t.Fatalf("failure diagnostics were not persisted: %#v", summary)
			}
			if summary.PrimaryClassification != tt.want {
				t.Fatalf("unexpected summary classification: got %q want %q", summary.PrimaryClassification, tt.want)
			}

			store := tundiag.Store{RuntimeDir: runtimeDir}
			loaded, loadedPath, err := store.Load()
			if err != nil {
				t.Fatalf("load persisted failure diagnostics: %v", err)
			}
			assertPersistedTunLifecycleFailure(t, loaded, loadedPath, summary.ReportPath, tt.phase, "pending", tt.want, secret)

			manager.finalizeTunFailureDiagnosticRollback(context.Background(), summary, "completed")
			finalized, finalizedPath, err := store.Load()
			if err != nil {
				t.Fatalf("reload finalized failure diagnostics: %v", err)
			}
			assertPersistedTunLifecycleFailure(t, finalized, finalizedPath, summary.ReportPath, tt.phase, "completed", tt.want, secret)
		})
	}
}

func assertPersistedTunLifecycleFailure(
	t *testing.T,
	report tundiag.Report,
	loadedPath string,
	wantPath string,
	wantPhase string,
	wantRollback string,
	wantClassification tundiag.Classification,
	secret string,
) {
	t.Helper()
	if loadedPath != wantPath || report.ReportPath != wantPath {
		t.Fatalf("unexpected persisted report path: loaded=%q model=%q want=%q", loadedPath, report.ReportPath, wantPath)
	}
	if !report.Historical {
		t.Fatalf("reloaded failure report must be historical: %#v", report)
	}
	if report.FailurePhase != wantPhase || report.RollbackStatus != wantRollback {
		t.Fatalf("unexpected lifecycle metadata: %#v", report)
	}
	if report.PrimaryClassification != wantClassification {
		t.Fatalf("unexpected persisted classification: got %q want %q", report.PrimaryClassification, wantClassification)
	}
	for _, invalid := range []tundiag.Classification{
		tundiag.Classification(tundiag.StatusHealthy),
		tundiag.Classification(tundiag.StatusDegraded),
		tundiag.Classification(tundiag.StatusUnhealthy),
		tundiag.Classification(tundiag.StatusUnavailable),
	} {
		if report.PrimaryClassification == invalid {
			t.Fatalf("overall status leaked into classification taxonomy: %#v", report)
		}
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal persisted report: %v", err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "vless://") {
		t.Fatalf("persisted lifecycle report leaked sensitive cause: %s", encoded)
	}
}
