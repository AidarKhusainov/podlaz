package daemon

import (
	"context"
	"errors"
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

func TestValidateE2ETunHookConfigAcceptsDocumentedPhases(t *testing.T) {
	for _, phase := range []string{
		e2eTunHookTunAddressApplyPhase,
		e2eTunHookRouteApplyPhase,
		e2eTunHookDNSApplyPhase,
		e2eTunHookNetworkVerifyPhase,
		e2eTunHookDNSInactiveScopePhase,
		e2eTunHookBeforeCommitPausePhase,
	} {
		t.Run(phase, func(t *testing.T) {
			t.Setenv(e2eTunHookGateEnv, "true")
			t.Setenv(e2eTunHookPhaseEnv, phase)
			if err := validateE2ETunHookConfig(); err != nil {
				t.Fatalf("validate documented E2E hook phase %q: %v", phase, err)
			}
		})
	}
}

func TestE2ETunAddressHookFailsAfterDelegateApplyAndPreservesOwnership(t *testing.T) {
	t.Setenv(e2eTunHookGateEnv, "true")
	t.Setenv(e2eTunHookPhaseEnv, e2eTunHookTunAddressApplyPhase)
	t.Setenv(e2eTunHookDirEnv, t.TempDir())

	address := &e2eHookApplyRecordingTunAddressExecutor{}
	executor := maybeWrapE2ETunHookExecutor(netexecutor.DNSAwareTunExecutor{
		Base: netexecutor.TunExecutor{
			TunDevice:   e2eHookApplyStaticTunDeviceExecutor{},
			TunAddress:  address,
			Routes:      recordingRouteExecutor{},
			PolicyRules: e2eHookApplyStaticPolicyRuleExecutor{},
		},
		DNS: recordingDNSExecutor{},
	})
	steps, err := executor.Apply(context.Background(), planner.TunPlan{
		TunDevice: planner.TunDevicePlan{Name: "podlaz0", Action: "verify"},
		TunAddress: planner.TunAddressPlan{
			Family:    "ipv4",
			Interface: "podlaz0",
			CIDR:      planner.DefaultTunIPv4CIDR,
			Action:    planner.TunAddressActionAssign,
			Owner:     planner.TunAddressOwner,
		},
		DNS: planner.TunDNSPlan{
			Backend:    planner.DNSBackendSystemdResolved,
			TargetLink: "podlaz0",
			Servers:    []string{"192.0.2.53"},
			Action:     planner.DNSActionConfigure,
		},
	})
	if err == nil || !errors.Is(err, netexecutor.ErrTunAddressApply) {
		t.Fatalf("expected typed post-mutation address failure, got %v", err)
	}
	if address.applyCalls != 1 {
		t.Fatalf("address delegate Apply calls = %d, want 1", address.applyCalls)
	}
	if len(steps) != 1 || steps[0].Kind != "tun-address" || steps[0].Owner != netexecutor.OwnerTunAddress {
		t.Fatalf("partial address ownership was not preserved: %#v", steps)
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
	step, err := executor.Apply(context.Background(), planner.TunDNSPlan{TargetLink: "podlaz0", Action: planner.DNSActionConfigure, Servers: []string{"192.0.2.53"}})
	if err == nil || !strings.Contains(err.Error(), "DNS apply") {
		t.Fatalf("expected DNS hook error, got %v", err)
	}
	if step.Kind != "dns" || step.Owner != netexecutor.OwnerDNS {
		t.Fatalf("expected DNS step to be preserved, got %#v", step)
	}
}

func TestE2ENetworkVerifyHookFailsAfterDelegateVerification(t *testing.T) {
	delegate := &recordingTunPlanExecutor{}
	executor := e2eHookNetworkVerifyExecutor{delegate: delegate}
	if err := executor.Verify(context.Background(), planner.TunPlan{}); err == nil || !strings.Contains(err.Error(), "network verification") {
		t.Fatalf("expected post-verification E2E failure, got %v", err)
	}
	if delegate.verifyCalls != 1 {
		t.Fatalf("delegate verify calls = %d, want 1", delegate.verifyCalls)
	}
}

func TestReplaceResolvedCurrentScopesChangesOnlyTargetLink(t *testing.T) {
	input := `Link 2 (eth0)
    Current Scopes: DNS

Link 7 (podlaz0)
    Current Scopes: DNS
         Protocols: +DefaultRoute
       DNS Servers: 192.0.2.53
        DNS Domain: ~.`
	got, replaced := replaceResolvedCurrentScopes(input, "podlaz0", "none")
	if !replaced {
		t.Fatal("expected target Current Scopes replacement")
	}
	if !strings.Contains(got, "Link 2 (eth0)\n    Current Scopes: DNS") {
		t.Fatalf("foreign link scope changed unexpectedly:\n%s", got)
	}
	if !strings.Contains(got, "Link 7 (podlaz0)\n    Current Scopes: none") {
		t.Fatalf("target link scope was not replaced:\n%s", got)
	}
	for _, want := range []string{"+DefaultRoute", "DNS Servers: 192.0.2.53", "DNS Domain: ~."} {
		if !strings.Contains(got, want) {
			t.Fatalf("resolved configuration evidence %q was lost:\n%s", want, got)
		}
	}
}

func TestInactiveScopeRunnerUsesProductionCommandResult(t *testing.T) {
	delegate := staticExecutorCommandRunner{result: netexecutor.CommandResult{Stdout: `Link 7 (podlaz0)
    Current Scopes: DNS
         Protocols: +DefaultRoute
       DNS Servers: 192.0.2.53
        DNS Domain: ~.`}}
	runner := e2eInactiveScopeCommandRunner{delegate: delegate}
	result, err := runner.Run(context.Background(), "resolvectl", "status", "--no-pager")
	if err != nil {
		t.Fatalf("run synthetic inactive-scope status: %v", err)
	}
	if !strings.Contains(result.Stdout, "Current Scopes: none") {
		t.Fatalf("synthetic status did not expose inactive scope:\n%s", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "+DefaultRoute") || !strings.Contains(result.Stdout, "DNS Domain: ~.") {
		t.Fatalf("synthetic status lost configured ownership evidence:\n%s", result.Stdout)
	}
}

type recordingRouteExecutor struct{}

func (recordingRouteExecutor) Add(context.Context, planner.TunRoutePlan) (netexecutor.Step, error) {
	return netexecutor.Step{Kind: "route", Target: "default", Owner: netexecutor.OwnerRoute}, nil
}

func (recordingRouteExecutor) Verify(context.Context, planner.TunRoutePlan) error   { return nil }
func (recordingRouteExecutor) Rollback(context.Context, planner.TunRoutePlan) error { return nil }

type recordingDNSExecutor struct{}

func (recordingDNSExecutor) Apply(context.Context, planner.TunDNSPlan) (netexecutor.Step, error) {
	return netexecutor.Step{Kind: "dns", Target: "podlaz0", Owner: netexecutor.OwnerDNS}, nil
}

func (recordingDNSExecutor) Verify(context.Context, planner.TunDNSPlan) error   { return nil }
func (recordingDNSExecutor) Rollback(context.Context, planner.TunDNSPlan) error { return nil }

type recordingTunPlanExecutor struct {
	verifyCalls int
}

func (*recordingTunPlanExecutor) Apply(context.Context, planner.TunPlan) ([]netexecutor.Step, error) {
	return nil, nil
}

func (e *recordingTunPlanExecutor) Verify(context.Context, planner.TunPlan) error {
	e.verifyCalls++
	return nil
}

func (*recordingTunPlanExecutor) Rollback(context.Context, planner.TunPlan) error { return nil }

type staticExecutorCommandRunner struct {
	result netexecutor.CommandResult
	err    error
}

func (r staticExecutorCommandRunner) Run(context.Context, string, ...string) (netexecutor.CommandResult, error) {
	return r.result, r.err
}

type e2eHookApplyStaticTunDeviceExecutor struct {
	step netexecutor.Step
}

func (e e2eHookApplyStaticTunDeviceExecutor) Create(context.Context, planner.TunDevicePlan) (netexecutor.Step, error) {
	return e.step, nil
}

func (e2eHookApplyStaticTunDeviceExecutor) Verify(context.Context, planner.TunDevicePlan) error {
	return nil
}

func (e2eHookApplyStaticTunDeviceExecutor) Rollback(context.Context, planner.TunDevicePlan) error {
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

type e2eHookApplyRecordingTunAddressExecutor struct {
	applyCalls int
}

func (e *e2eHookApplyRecordingTunAddressExecutor) Bind(_ context.Context, plan planner.TunAddressPlan, _ netexecutor.TunLinkCreationProof) (planner.TunAddressPlan, error) {
	return plan, nil
}

func (e *e2eHookApplyRecordingTunAddressExecutor) Apply(context.Context, planner.TunAddressPlan) (netexecutor.Step, error) {
	e.applyCalls++
	return netexecutor.Step{Kind: "tun-address", Target: "podlaz0 " + planner.DefaultTunIPv4CIDR, Owner: netexecutor.OwnerTunAddress}, nil
}

func (*e2eHookApplyRecordingTunAddressExecutor) Verify(context.Context, planner.TunAddressPlan) error {
	return nil
}

func (*e2eHookApplyRecordingTunAddressExecutor) Rollback(context.Context, planner.TunAddressPlan) error {
	return nil
}
