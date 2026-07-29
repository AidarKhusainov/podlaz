package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestFullTunnelTransactionRunnerCapturesNetworkFailureDiagnosticsBeforeRollback(t *testing.T) {
	tests := []struct {
		name      string
		phase     string
		wantOrder []string
	}{
		{
			name:      "network apply",
			phase:     "network-apply",
			wantOrder: []string{"apply", "diagnostics", "rollback", "stop-core", "finalize-completed"},
		},
		{
			name:      "network verify",
			phase:     "network-verify",
			wantOrder: []string{"apply", "verify", "diagnostics", "rollback", "stop-core", "finalize-completed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cause := errors.New(tt.phase + " failed")
			var events []string
			executor := &orderedNetworkFailureExecutor{
				phase:  tt.phase,
				cause:  cause,
				events: &events,
			}
			runtimeDir := t.TempDir()
			runner := &fullTunnelTransactionRunner{
				runtimeDir: runtimeDir,
				profile: profile.Profile{
					ID:   "example-profile",
					Name: "Example Profile",
				},
				plan: transactionPlanForTest(),
				corePlan: tunCoreRuntimePlan{
					RuntimeConfigPath: filepath.Join(runtimeDir, generatedDirName, generatedXrayName),
					Status:            "test TUN core runtime",
				},
				executor: executor,
				now:      fixedClock(),
				startCore: func(context.Context) (fullTunnelCoreHandle, error) {
					return fullTunnelCoreHandle{done: make(chan struct{})}, nil
				},
				stopCore: func(fullTunnelCoreHandle) error {
					events = append(events, "stop-core")
					return nil
				},
				verifyCoreStarted: func(<-chan struct{}) error { return nil },
				collectFailureDiagnostics: func(_ context.Context, transactionID string, _ planner.TunPlan, gotCause error) tunFailureDiagnosticSummary {
					events = append(events, "diagnostics")
					if transactionID == "" {
						t.Fatal("diagnostics did not receive the transaction id")
					}
					if !errors.Is(gotCause, cause) {
						t.Fatalf("diagnostics received the wrong cause: %v", gotCause)
					}
					return tunFailureDiagnosticSummary{
						PrimaryClassification: tundiag.ClassDNSApplyFailure,
						ReportPath:            "/run/podlaz/diagnostics/tun-last.json",
						Persisted:             true,
					}
				},
				finalizeFailureDiagnostics: func(_ context.Context, summary tunFailureDiagnosticSummary, status string) {
					if !summary.Persisted || status != "completed" {
						t.Fatalf("unexpected diagnostic finalization: summary=%#v status=%q", summary, status)
					}
					events = append(events, "finalize-"+status)
				},
			}

			_, err := runner.run(context.Background())
			if !errors.Is(err, cause) {
				t.Fatalf("expected original network failure, got %v", err)
			}
			if !reflect.DeepEqual(events, tt.wantOrder) {
				t.Fatalf("unexpected event order: got %v want %v", events, tt.wantOrder)
			}
			if phase, _, rollback := tunFailureLogFields(err); phase != tt.phase || rollback != "completed" {
				t.Fatalf("unexpected lifecycle failure metadata: phase=%q rollback=%q err=%v", phase, rollback, err)
			}
			if !strings.Contains(err.Error(), "dns_apply_failure") || !strings.Contains(err.Error(), "doctor --tun") {
				t.Fatalf("network failure must include classification and doctor guidance: %v", err)
			}
			summaries, warnings := transactionStatuses(runtimeDir)
			if len(warnings) != 0 || len(summaries) != 0 {
				t.Fatalf("successful rollback must remove the failed transaction: summaries=%#v warnings=%#v", summaries, warnings)
			}
		})
	}
}

type orderedNetworkFailureExecutor struct {
	phase  string
	cause  error
	events *[]string
}

func (e *orderedNetworkFailureExecutor) Apply(_ context.Context, plan planner.TunPlan) ([]netexecutor.Step, error) {
	*e.events = append(*e.events, "apply")
	steps := []netexecutor.Step{{Kind: "tun-device", Target: plan.TunDevice.Name, Owner: netexecutor.OwnerTunDevice}}
	if e.phase == "network-apply" {
		return steps, e.cause
	}
	return steps, nil
}

func (e *orderedNetworkFailureExecutor) Verify(context.Context, planner.TunPlan) error {
	*e.events = append(*e.events, "verify")
	if e.phase == "network-verify" {
		return e.cause
	}
	return nil
}

func (e *orderedNetworkFailureExecutor) Rollback(context.Context, planner.TunPlan) error {
	*e.events = append(*e.events, "rollback")
	return nil
}
