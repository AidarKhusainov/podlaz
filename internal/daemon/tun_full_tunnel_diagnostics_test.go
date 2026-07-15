package daemon

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestFullTunnelTransactionRunnerCollectsDiagnosticsBeforeRollback(t *testing.T) {
	runner := postApplyRollbackRunnerForTest(t.TempDir(), &postApplyRollbackPlanExecutor{})
	var events []string
	runner.verifyConnectivity = func(context.Context, planner.TunPlan, tunCoreRuntimePlan) error {
		events = append(events, "verify")
		return errors.New("connectivity failed")
	}
	runner.collectFailureDiagnostics = func(_ context.Context, transactionID string, _ planner.TunPlan, cause error) string {
		events = append(events, "diagnostics")
		if transactionID == "" || cause == nil {
			t.Fatal("diagnostics did not receive transaction context")
		}
		return "TUN diagnostics: dns_udp_failure; last report: /run/podlaz/diagnostics/tun-last.json"
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
