package daemon

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestFullTunnelTransactionRunnerCollectsDiagnosticsBeforeRollback(t *testing.T) {
	runner := postApplyRollbackRunnerForTest(t.TempDir(), &postApplyRollbackPlanExecutor{})
	var events []string
	runner.verifyConnectivity = func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error {
		events = append(events, "verify")
		return errors.New("connectivity failed")
	}
	runner.collectFailureDiagnostics = func(_ context.Context, transactionID string, _ planner.TunPlan, cause error) tunFailureDiagnosticSummary {
		events = append(events, "diagnostics")
		if transactionID == "" || cause == nil {
			t.Fatal("diagnostics did not receive transaction context")
		}
		return tunFailureDiagnosticSummary{
			PrimaryClassification: tundiag.ClassDNSUDPFailure,
			ReportPath:            "/run/podlaz/diagnostics/tun-last.json",
			Persisted:             true,
		}
	}
	runner.rollbackTransaction = func(context.Context, string, planner.TunPlan, tunPlanExecutor) error {
		events = append(events, "rollback")
		return nil
	}

	_, err := runner.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "dns_udp_failure") {
		t.Fatalf("expected diagnostic summary in error, got %v", err)
	}
	if want := []string{"verify", "diagnostics", "rollback"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("unexpected event order: got %v want %v", events, want)
	}
}

func TestFullTunnelTransactionRunnerRollbackOutlivesCancelledRequest(t *testing.T) {
	runner := postApplyRollbackRunnerForTest(t.TempDir(), &postApplyRollbackPlanExecutor{})
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	runner.verifyConnectivity = func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error {
		cancelRequest()
		return errors.New("connectivity failed")
	}
	runner.collectFailureDiagnostics = func(ctx context.Context, _ string, _ planner.TunPlan, _ error) tunFailureDiagnosticSummary {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("diagnostics must inherit request cancellation, got %v", ctx.Err())
		}
		return tunFailureDiagnosticSummary{}
	}
	rollbackCalled := false
	runner.rollbackTransaction = func(ctx context.Context, _ string, _ planner.TunPlan, _ tunPlanExecutor) error {
		rollbackCalled = true
		if err := ctx.Err(); err != nil {
			t.Fatalf("cleanup context inherited request cancellation: %v", err)
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("cleanup context has no bounded deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > tunRollbackCleanupTimeout {
			t.Fatalf("unexpected cleanup deadline: %s", remaining)
		}
		return nil
	}

	_, err := runner.run(requestCtx)
	if err == nil || !rollbackCalled {
		t.Fatalf("expected mandatory rollback after cancellation, err=%v called=%t", err, rollbackCalled)
	}
}
