package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	OwnerTunDevice  = "podlaz:tun-device"
	OwnerRoute      = "podlaz:route"
	OwnerPolicyRule = "podlaz:policy-rule"
)

type TunDeviceExecutor interface {
	Create(ctx context.Context, plan planner.TunDevicePlan) (Step, error)
	Verify(ctx context.Context, plan planner.TunDevicePlan) error
	Rollback(ctx context.Context, plan planner.TunDevicePlan) error
}

type RouteExecutor interface {
	Add(ctx context.Context, plan planner.TunRoutePlan) (Step, error)
	Verify(ctx context.Context, plan planner.TunRoutePlan) error
	Rollback(ctx context.Context, plan planner.TunRoutePlan) error
}

type PolicyRuleExecutor interface {
	Add(ctx context.Context, plan planner.TunPolicyRulePlan) (Step, error)
	Verify(ctx context.Context, plan planner.TunPolicyRulePlan) error
	Rollback(ctx context.Context, plan planner.TunPolicyRulePlan) error
}

type Step struct {
	Kind        string
	Target      string
	Description string
	Owner       string
}

type TunExecutor struct {
	TunDevice   TunDeviceExecutor
	TunAddress  TunAddressExecutor
	Routes      RouteExecutor
	PolicyRules PolicyRuleExecutor
}

func NewOSExecutor() TunExecutor {
	return newTunExecutorWithRunner(OSRunner{})
}

func newTunExecutorWithRunner(runner CommandRunner) TunExecutor {
	if runner == nil {
		runner = OSRunner{}
	}
	return TunExecutor{
		TunDevice:   IPTunDeviceExecutor{Runner: runner, DeviceUser: defaultTunDeviceUser, DeviceGroup: defaultTunDeviceGroup},
		TunAddress:  IPTunAddressExecutor{Runner: runner},
		Routes:      IPRouteExecutor{Runner: runner},
		PolicyRules: IPPolicyRuleExecutor{Runner: runner},
	}
}

type AppliedStepSink func(Step) error

func (e TunExecutor) Apply(ctx context.Context, plan planner.TunPlan) ([]Step, error) {
	return e.ApplyWithStepSink(ctx, plan, nil)
}

// ApplyWithStepSink reports each exact ownership step immediately after the
// corresponding host mutation and before the next resource can be changed.
// The caller can therefore make rollback authority durable without opening a
// crash window across the composite apply sequence.
func (e TunExecutor) ApplyWithStepSink(ctx context.Context, plan planner.TunPlan, sink AppliedStepSink) ([]Step, error) {
	if err := e.validatePlan(plan); err != nil {
		return nil, err
	}
	steps := make([]Step, 0, 1+len(plan.Routes)+len(plan.PolicyRules))

	record := func(step Step, applyErr error) error {
		var persistErr error
		steps, persistErr = recordAppliedStep(steps, step, sink)
		return errors.Join(applyErr, persistErr)
	}

	switch tunDeviceAction(plan.TunDevice.Action) {
	case "", "create":
		step, applyErr := e.TunDevice.Create(ctx, plan.TunDevice)
		if err := record(step, applyErr); err != nil {
			return steps, err
		}
	case "verify", "use-existing":
		if err := e.TunDevice.Verify(ctx, plan.TunDevice); err != nil {
			return steps, err
		}
	default:
		return steps, fmt.Errorf("unsupported TUN device action %q", plan.TunDevice.Action)
	}
	if shouldApplyTunAddress(plan.TunAddress) {
		step, applyErr := e.TunAddress.Apply(ctx, plan.TunAddress)
		if err := record(step, applyErr); err != nil {
			return steps, err
		}
	}

	for _, route := range plan.Routes {
		if route.Action != "add" {
			continue
		}
		step, applyErr := e.Routes.Add(ctx, route)
		if err := record(step, applyErr); err != nil {
			return steps, err
		}
	}
	for _, rule := range plan.PolicyRules {
		if rule.Action != "add" {
			continue
		}
		step, applyErr := e.PolicyRules.Add(ctx, rule)
		if err := record(step, applyErr); err != nil {
			return steps, err
		}
	}
	return steps, nil
}

func (e TunExecutor) Verify(ctx context.Context, plan planner.TunPlan) error {
	if err := e.validatePlan(plan); err != nil {
		return err
	}
	if err := e.TunDevice.Verify(ctx, plan.TunDevice); err != nil {
		return err
	}
	if shouldApplyTunAddress(plan.TunAddress) {
		if err := e.TunAddress.Verify(ctx, plan.TunAddress); err != nil {
			return err
		}
	}
	for _, route := range plan.Routes {
		if route.Action != "add" {
			continue
		}
		if err := e.Routes.Verify(ctx, route); err != nil {
			return err
		}
	}
	for _, rule := range plan.PolicyRules {
		if rule.Action != "add" {
			continue
		}
		if err := e.PolicyRules.Verify(ctx, rule); err != nil {
			return err
		}
	}
	return nil
}

func (e TunExecutor) Rollback(ctx context.Context, plan planner.TunPlan) error {
	if err := e.validatePlan(plan); err != nil {
		return err
	}
	var errs []error
	for i := len(plan.PolicyRules) - 1; i >= 0; i-- {
		rule := plan.PolicyRules[i]
		if rule.Action != "add" {
			continue
		}
		if err := e.PolicyRules.Rollback(ctx, rule); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(plan.Routes) - 1; i >= 0; i-- {
		route := plan.Routes[i]
		if route.Action != "add" {
			continue
		}
		if err := e.Routes.Rollback(ctx, route); err != nil {
			errs = append(errs, err)
		}
	}
	if shouldApplyTunAddress(plan.TunAddress) {
		if err := e.TunAddress.Rollback(ctx, plan.TunAddress); err != nil {
			errs = append(errs, err)
		}
	}
	switch tunDeviceAction(plan.TunDevice.Action) {
	case "", "create":
		if strings.TrimSpace(plan.TunDevice.Name) != "" {
			if err := e.TunDevice.Rollback(ctx, plan.TunDevice); err != nil {
				errs = append(errs, err)
			}
		}
	case "verify", "use-existing":
	default:
		errs = append(errs, fmt.Errorf("unsupported TUN device action %q", plan.TunDevice.Action))
	}
	return errors.Join(errs...)
}

func appendAppliedStep(steps []Step, step Step) []Step {
	if strings.TrimSpace(step.Kind) == "" {
		return steps
	}
	return append(steps, step)
}

func recordAppliedStep(steps []Step, step Step, sink AppliedStepSink) ([]Step, error) {
	if strings.TrimSpace(step.Kind) == "" {
		return steps, nil
	}
	steps = append(steps, step)
	if sink == nil {
		return steps, nil
	}
	if err := sink(step); err != nil {
		return steps, fmt.Errorf("persist applied %s ownership: %w", step.Kind, err)
	}
	return steps, nil
}

func tunDeviceAction(action string) string {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "add" {
		return "create"
	}
	return action
}

func (e TunExecutor) validatePlan(plan planner.TunPlan) error {
	if e.TunDevice == nil {
		return errors.New("missing TUN device executor")
	}
	if e.Routes == nil {
		return errors.New("missing route executor")
	}
	if e.PolicyRules == nil {
		return errors.New("missing policy-rule executor")
	}
	if strings.TrimSpace(plan.TunAddress.CIDR) != "" && e.TunAddress == nil {
		return errors.New("missing TUN address executor")
	}
	return nil
}

func shouldApplyTunAddress(plan planner.TunAddressPlan) bool {
	return strings.TrimSpace(plan.CIDR) != "" && plan.Action == planner.TunAddressActionAssign
}

func (e TunExecutor) BindTunAddress(ctx context.Context, plan planner.TunPlan) (planner.TunPlan, error) {
	if strings.TrimSpace(plan.TunAddress.CIDR) == "" {
		return plan, nil
	}
	if e.TunAddress == nil {
		return plan, errors.New("missing TUN address executor")
	}
	bound, err := e.TunAddress.Bind(ctx, plan.TunAddress)
	if err != nil {
		return plan, err
	}
	plan.TunAddress = bound
	return plan, nil
}
