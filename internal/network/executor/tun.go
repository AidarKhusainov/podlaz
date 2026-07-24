package executor

import (
	"context"
	"fmt"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

type Step struct {
	Kind        string
	Target      string
	Description string
	Owner       string
}

const (
	OwnerTunDevice  = "podlaz:tun-device"
	OwnerRoute      = "podlaz:route"
	OwnerPolicyRule = "podlaz:policy-rule"
	OwnerDNS        = "podlaz:dns"
	OwnerNFTables   = "podlaz:nftables"
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

type TunExecutor struct {
	Device TunDeviceExecutor
	Route  RouteExecutor
	Rule   PolicyRuleExecutor
}

func NewTunExecutor() TunExecutor {
	return newTunExecutorWithRunner(nil)
}

func newTunExecutorWithRunner(runner CommandRunner) TunExecutor {
	if runner == nil {
		runner = OSRunner{}
	}
	return TunExecutor{
		Device: IPTunDeviceExecutor{Runner: runner, DeviceUser: defaultTunDeviceUser, DeviceGroup: defaultTunDeviceGroup},
		Route:  IPRouteExecutor{Runner: runner},
		Rule:   IPPolicyRuleExecutor{Runner: runner},
	}
}

func (e TunExecutor) Apply(ctx context.Context, plan planner.TunPlan) ([]Step, error) {
	if e.Device == nil || e.Route == nil || e.Rule == nil {
		return nil, fmt.Errorf("incomplete TUN executor")
	}
	var steps []Step
	if plan.TunDevice.Action == "create" {
		step, err := e.Device.Create(ctx, plan.TunDevice)
		steps = appendAppliedStep(steps, step)
		if err != nil {
			return steps, err
		}
	} else {
		if err := e.Device.Verify(ctx, plan.TunDevice); err != nil {
			return steps, err
		}
	}
	for _, route := range plan.Routes {
		if route.Action != "add" {
			continue
		}
		step, err := e.Route.Add(ctx, route)
		steps = appendAppliedStep(steps, step)
		if err != nil {
			return steps, err
		}
	}
	for _, rule := range plan.PolicyRules {
		if rule.Action != "add" {
			continue
		}
		step, err := e.Rule.Add(ctx, rule)
		steps = appendAppliedStep(steps, step)
		if err != nil {
			return steps, err
		}
	}
	return steps, nil
}

func appendAppliedStep(steps []Step, step Step) []Step {
	if step.Kind == "" {
		return steps
	}
	return append(steps, step)
}

func (e TunExecutor) Verify(ctx context.Context, plan planner.TunPlan) error {
	if err := e.Device.Verify(ctx, plan.TunDevice); err != nil {
		return err
	}
	for _, route := range plan.Routes {
		if route.Action == "add" {
			if err := e.Route.Verify(ctx, route); err != nil {
				return err
			}
		}
	}
	for _, rule := range plan.PolicyRules {
		if rule.Action == "add" {
			if err := e.Rule.Verify(ctx, rule); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e TunExecutor) Rollback(ctx context.Context, plan planner.TunPlan) error {
	var first error
	for i := len(plan.PolicyRules) - 1; i >= 0; i-- {
		if err := e.Rule.Rollback(ctx, plan.PolicyRules[i]); err != nil && first == nil {
			first = err
		}
	}
	for i := len(plan.Routes) - 1; i >= 0; i-- {
		if err := e.Route.Rollback(ctx, plan.Routes[i]); err != nil && first == nil {
			first = err
		}
	}
	if plan.TunDevice.Name != "" {
		if err := e.Device.Rollback(ctx, plan.TunDevice); err != nil && first == nil {
			first = err
		}
	}
	return first
}
