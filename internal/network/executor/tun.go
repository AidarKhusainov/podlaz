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
	Routes      RouteExecutor
	PolicyRules PolicyRuleExecutor
}

func NewOSExecutor() TunExecutor {
	runner := OSRunner{}
	return TunExecutor{
		TunDevice:   IPTunDeviceExecutor{Runner: runner, DeviceUser: defaultTunDeviceUser, DeviceGroup: defaultTunDeviceGroup},
		Routes:      IPRouteExecutor{Runner: runner},
		PolicyRules: IPPolicyRuleExecutor{Runner: runner},
	}
}

func (e TunExecutor) Apply(ctx context.Context, plan planner.TunPlan) ([]Step, error) {
	if err := e.validate(); err != nil {
		return nil, err
	}
	steps := make([]Step, 0, 1+len(plan.Routes)+len(plan.PolicyRules))

	switch tunDeviceAction(plan.TunDevice.Action) {
	case "", "create":
		step, err := e.TunDevice.Create(ctx, plan.TunDevice)
		if err != nil {
			return steps, err
		}
		steps = appendAppliedStep(steps, step)
	case "verify", "use-existing":
		if err := e.TunDevice.Verify(ctx, plan.TunDevice); err != nil {
			return steps, err
		}
	default:
		return steps, fmt.Errorf("unsupported TUN device action %q", plan.TunDevice.Action)
	}

	for _, route := range plan.Routes {
		if route.Action != "add" {
			continue
		}
		step, err := e.Routes.Add(ctx, route)
		if err != nil {
			return steps, err
		}
		steps = appendAppliedStep(steps, step)
	}
	for _, rule := range plan.PolicyRules {
		if rule.Action != "add" {
			continue
		}
		step, err := e.PolicyRules.Add(ctx, rule)
		if err != nil {
			return steps, err
		}
		steps = appendAppliedStep(steps, step)
	}
	return steps, nil
}

func (e TunExecutor) Verify(ctx context.Context, plan planner.TunPlan) error {
	if err := e.validate(); err != nil {
		return err
	}
	if err := e.TunDevice.Verify(ctx, plan.TunDevice); err != nil {
		return err
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
	if err := e.validate(); err != nil {
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

func tunDeviceAction(action string) string {
	return strings.ToLower(strings.TrimSpace(action))
}

func (e TunExecutor) validate() error {
	if e.TunDevice == nil {
		return errors.New("missing TUN device executor")
	}
	if e.Routes == nil {
		return errors.New("missing route executor")
	}
	if e.PolicyRules == nil {
		return errors.New("missing policy-rule executor")
	}
	return nil
}
