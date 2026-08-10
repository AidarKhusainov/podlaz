package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestTunRevalidationVerificationFailureProducesTerminalCleanupOutcome(t *testing.T) {
	outcome := issue245TerminalOutcome(t, errors.New("synthetic TLS failure"))
	if !outcome.needsLifecycleCleanup() {
		t.Fatalf("verification failure outcome=%#v, want lifecycle cleanup", outcome)
	}
	if outcome.classification != api.TunHealthConnectivityFailed {
		t.Fatalf("classification=%q, want %q", outcome.classification, api.TunHealthConnectivityFailed)
	}
}

func TestTunRevalidationTimeoutProducesTerminalCleanupOutcome(t *testing.T) {
	outcome := issue245TerminalOutcome(t, context.DeadlineExceeded)
	if !outcome.needsLifecycleCleanup() {
		t.Fatalf("timeout outcome=%#v, want lifecycle cleanup", outcome)
	}
	if outcome.classification != api.TunHealthRevalidationTimeout {
		t.Fatalf("classification=%q, want %q", outcome.classification, api.TunHealthRevalidationTimeout)
	}
}

func TestTunRevalidationCancellationDoesNotProduceRecursiveCleanupOutcome(t *testing.T) {
	outcome := issue245TerminalOutcome(t, context.Canceled)
	if outcome.needsLifecycleCleanup() {
		t.Fatalf("caller cancellation outcome=%#v unexpectedly requested a second disconnect", outcome)
	}
}

func TestTunRevalidationTerminalHandlerPersistsDiagnosticsBeforeBoundedCleanup(t *testing.T) {
	outcome := tunRevalidationOutcome{
		terminal:       true,
		cause:          newTunRevalidationVerificationError(api.TunHealthConnectivityFailed, newTunVerificationError("tls", "TLS revalidation failed", errors.New("synthetic TLS failure"))),
		plan:           planner.TunPlan{Mode: planner.ModeTun},
		generation:     2,
		classification: api.TunHealthConnectivityFailed,
	}
	var order []string
	handler := tunRevalidationTerminalHandler{
		collect: func(ctx context.Context, plan planner.TunPlan, cause error) tunFailureDiagnosticSummary {
			order = append(order, "diagnostics")
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("diagnostic collection is not bounded")
			}
			if plan.Mode != planner.ModeTun || cause == nil {
				t.Fatalf("diagnostic input lost terminal evidence: plan=%#v cause=%v", plan, cause)
			}
			return tunFailureDiagnosticSummary{ReportPath: "/run/podlaz/diagnostics/tun-latest.json", Persisted: true}
		},
		disconnect: func(ctx context.Context) error {
			order = append(order, "disconnect")
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > tunRollbackCleanupTimeout {
				t.Fatalf("cleanup deadline is not bounded by %s: %v", tunRollbackCleanupTimeout, deadline)
			}
			return nil
		},
		finalize: func(_ context.Context, summary tunFailureDiagnosticSummary, status string) {
			order = append(order, "finalize:"+status)
			if !summary.Persisted {
				t.Fatal("finalization lost persisted diagnostic summary")
			}
		},
	}

	handler.Handle(context.Background(), outcome)
	want := []string{"diagnostics", "disconnect", "finalize:completed"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("terminal ordering=%v, want %v", order, want)
	}
}

func TestTunRevalidationCleanupFailurePublishesCleanupRequired(t *testing.T) {
	runtime := &tunRevalidationRuntime{health: &api.TunHealthStatus{
		State:             api.TunHealthDegraded,
		NetworkGeneration: 3,
		Classification:    api.TunHealthConnectivityFailed,
	}}
	outcome := tunRevalidationOutcome{
		terminal:       true,
		cause:          errors.New("synthetic HTTPS failure"),
		generation:     3,
		classification: api.TunHealthConnectivityFailed,
	}
	var finalized string
	handler := tunRevalidationTerminalHandler{
		collect: func(context.Context, planner.TunPlan, error) tunFailureDiagnosticSummary {
			return tunFailureDiagnosticSummary{ReportPath: "/run/podlaz/diagnostics/tun-latest.json", Persisted: true}
		},
		disconnect:          func(context.Context) error { return errors.New("synthetic rollback failure") },
		finalize:            func(_ context.Context, _ tunFailureDiagnosticSummary, status string) { finalized = status },
		markCleanupRequired: runtime.MarkCleanupRequired,
	}

	handler.Handle(context.Background(), outcome)
	assertTunHealth(t, runtime.Health(), api.TunHealthCleanupRequired, 3, api.TunHealthConnectivityFailed)
	if finalized != "failed" {
		t.Fatalf("diagnostic rollback status=%q, want failed", finalized)
	}
}

func TestTunRevalidationTerminalHandlerDoesNotRunAfterShutdownCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	handler := tunRevalidationTerminalHandler{
		collect: func(context.Context, planner.TunPlan, error) tunFailureDiagnosticSummary {
			calls++
			return tunFailureDiagnosticSummary{}
		},
		disconnect: func(context.Context) error {
			calls++
			return nil
		},
	}
	handler.Handle(ctx, tunRevalidationOutcome{terminal: true, cause: errors.New("failure")})
	if calls != 0 {
		t.Fatalf("shutdown-cancelled terminal handler ran %d cleanup steps", calls)
	}
}

func TestTunRevalidationCoordinatorReleasesActiveTriggerBeforeTerminalCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var coordinator *tunRevalidationCoordinator
	coordinator = newTunRevalidationOutcomeCoordinator(
		func(context.Context, tunRevalidationTrigger) tunRevalidationOutcome {
			return tunRevalidationOutcome{terminal: true, cause: errors.New("failure")}
		},
		func(context.Context, tunRevalidationOutcome) {
			coordinator.InterruptForMutation()
			if trigger := coordinator.takePendingTrigger(); trigger != "" {
				t.Fatalf("terminal cleanup requeued completed trigger %q", trigger)
			}
			cancel()
		},
	)
	coordinator.Notify(tunRevalidationTriggerResume)
	coordinator.Run(ctx)
}

func TestTunRevalidationDiagnosticFailurePhasePreservesFailedLayer(t *testing.T) {
	cause := newTunRevalidationVerificationError(
		api.TunHealthConnectivityFailed,
		newTunVerificationError("https", "HTTPS revalidation failed", errors.New("synthetic HTTPS failure")),
	)
	if got := tunRevalidationDiagnosticFailurePhase(cause); got != "https" {
		t.Fatalf("failure phase=%q, want https", got)
	}
}

func issue245TerminalOutcome(t *testing.T, verificationErr error) tunRevalidationOutcome {
	t.Helper()
	baseline := tunUplinkFingerprint{Interface: "wlan0", InterfaceIndex: 3, Gateway: "192.0.2.1", Addresses: "192.0.2.55/24"}
	changed := baseline
	changed.Gateway = "192.0.2.254"
	inspectCalls := 0
	verifyCalls := 0
	runtime := newTunRevalidationRuntime(
		func(context.Context) (tunRevalidationObservation, error) {
			fingerprint := baseline
			if inspectCalls > 0 {
				fingerprint = changed
			}
			inspectCalls++
			return tunRevalidationObservation{fingerprint: fingerprint, plan: planner.TunPlan{Mode: planner.ModeTun}}, nil
		},
		func(context.Context, tunRevalidationObservation) error {
			verifyCalls++
			if verifyCalls == 1 {
				return nil
			}
			if verificationErr == nil {
				return nil
			}
			if errors.Is(verificationErr, context.Canceled) || errors.Is(verificationErr, context.DeadlineExceeded) {
				return verificationErr
			}
			return newTunRevalidationVerificationError(api.TunHealthConnectivityFailed, verificationErr)
		},
	)
	if err := runtime.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize runtime: %v", err)
	}
	return runtime.Revalidate(context.Background(), tunRevalidationTriggerResume)
}
