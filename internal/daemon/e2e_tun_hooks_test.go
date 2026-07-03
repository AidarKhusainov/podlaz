package daemon

import (
	"context"
	"strings"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestValidateE2ETunHookConfigRejectsUnknownPhase(t *testing.T) {
	t.Setenv(e2eTunHookGateEnv, "true")
	t.Setenv(e2eTunHookPhaseEnv, "unknown")

	err := validateE2ETunHookConfig()
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported hook phase error, got %v", err)
	}
}

func TestE2ERouteHookFailsBeforeDelegateApply(t *testing.T) {
	executor := e2eHookRouteExecutor{delegate: recordingRouteExecutor{}}
	step, err := executor.Add(context.Background(), planner.TunRoutePlan{Destination: "default", Table: planner.TunRoutingTable})
	if err == nil || !strings.Contains(err.Error(), "route apply") {
		t.Fatalf("expected route hook error, got %v", err)
	}
	if step.Kind != "" {
		t.Fatalf("expected no applied route step, got %#v", step)
	}
}

func TestE2EHookRouteFailureThroughTunExecutorApplyDoesNotApplyRoute(t *testing.T) {
	t.Parallel()

	routes := &e2eHookApplyRecordingRouteExecutor{
		step: netexecutor.Step{
			Kind:  "route",
			Owner: netexecutor.OwnerRoute,
		},
	}
	executor := netexecutor.TunExecutor{
		TunDevice: e2eHookApplyStaticTunDeviceExecutor{
			step: netexecutor.Step{
				Kind:  "tun-device",
				Owner: netexecutor.OwnerTunDevice,
			},
		},
		Routes:      e2eHookRouteExecutor{delegate: routes},
		PolicyRules: e2eHookApplyStaticPolicyRuleExecutor{},
	}

	steps, err := executor.Apply(context.Background(), planner.TunPlan{
		Routes: []planner.TunRoutePlan{{Action: "add"}},
	})
	if err == nil {
		t.Fatal("expected route hook error")
	}
	if !strings.Contains(err.Error(), "route apply failed before adding podlaz-owned route") {
		t.Fatalf("expected pre-apply hook error, got %v", err)
	}
	if routes.addCalls != 0 {
		t.Fatalf("route delegate Add calls = %d, want 0", routes.addCalls)
	}
	if got, want := len(steps), 1; got != want {
		t.Fatalf("applied steps = %d, want %d", got, want)
	}
	if steps[0].Owner != netexecutor.OwnerTunDevice {
		t.Fatalf("recorded step owner = %q, want %q", steps[0].Owner, netexecutor.OwnerTunDevice)
	}
}

func TestE2EDNSHookFailsAfterDelegateApply(t *testing.T) {
	executor := e2eHookDNSExecutor{delegate: recordingDNSExecutor{}}
	step, err := executor.Apply(context.Background(), planner.TunDNSPlan{TargetLink: "podlaz0", Action: planner.DNSActionConfigure, Servers: []string{"10.0.0.1"}})
	if err == nil || !strings.Contains(err.Error(), "DNS apply") {
		t.Fatalf("expected DNS hook error, got %v", err)
	}
	if step.Kind != "dns" || step.Owner != netexecutor.OwnerDNS {
		t.Fatalf("expected DNS step to be preserved, got %#v", step)
	}
}

type recordingRouteExecutor struct{}

func (recordingRouteExecutor) Add(context.Context, planner.TunRoutePlan) (netexecutor.Step, error) {
	return netexecutor.Step{Kind: "route", Target: "default", Owner: netexecutor.OwnerRoute}, nil
}

func (recordingRouteExecutor) Verify(context.Context, planner.TunRoutePlan) error { return nil }
func (recordingRouteExecutor) Rollback(context.Context, planner.TunRoutePlan) error { return nil }

type recordingDNSExecutor struct{}

func (recordingDNSExecutor) Apply(context.Context, planner.TunDNSPlan) (netexecutor.Step, error) {
	return netexecutor.Step{Kind: "dns", Target: "podlaz0", Owner: netexecutor.OwnerDNS}, nil
}

func (recordingDNSExecutor) Verify(context.Context, planner.TunDNSPlan) error { return nil }
func (recordingDNSExecutor) Rollback(context.Context, planner.TunDNSPlan) error { return nil }

type e2eHookApplyStaticTunDeviceExecutor struct {
	step netexecutor.Step
}

func (e e2eHookApplyStaticTunDeviceExecutor) Create(context.Context, planner.TunDevicePlan) (netexecutor.Step, error) {
	return e.step, nil
}

func (e e2eHookApplyStaticTunDeviceExecutor) Verify(context.Context, planner.TunDevicePlan) error {
	return nil
}

func (e e2eHookApplyStaticTunDeviceExecutor) Rollback(context.Context, planner.TunDevicePlan) error {
	return nil
}

type e2eHookApplyRecordingRouteExecutor struct {
	addCalls int
	step     netexecutor.Step
}

func (e *e2eHookApplyRecordingRouteExecutor) Add(context.Context, planner.TunRoutePlan) (netexecutor.Step, error) {
	e.addCalls++
	return e.step, nil
}

func (e *e2eHookApplyRecordingRouteExecutor) Verify(context.Context, planner.TunRoutePlan) error {
	return nil
}

func (e *e2eHookApplyRecordingRouteExecutor) Rollback(context.Context, planner.TunRoutePlan) error {
	return nil
}

type e2eHookApplyStaticPolicyRuleExecutor struct{}

func (e2eHookApplyStaticPolicyRuleExecutor) Add(context.Context, planner.TunPolicyRulePlan) (netexecutor.Step, error) {
	return netexecutor.Step{}, nil
}

func (e2eHookApplyStaticPolicyRuleExecutor) Verify(context.Context, planner.TunPolicyRulePlan) error {
	return nil
}

func (e2eHookApplyStaticPolicyRuleExecutor) Rollback(context.Context, planner.TunPolicyRulePlan) error {
	return nil
}
