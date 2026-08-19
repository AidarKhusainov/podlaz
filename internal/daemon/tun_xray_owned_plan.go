package daemon

import (
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func xrayOwnedTunPlan(plan planner.TunPlan) planner.TunPlan {
	plan.TunDevice.Action = "verify"
	plan.TunDevice.Reason = "Xray tun inbound owns podlaz0 creation and packet ingestion; podlaz verifies the link before applying routes, DNS, and firewall state"
	if _, err := planner.TunResourceAllocationFromPlan(plan); err == nil {
		if plan.TunAddress.Action == planner.TunAddressActionAssign {
			plan.TunAddress.Action = planner.TunAddressActionAssignExclusive
		}
		for i := range plan.Routes {
			if plan.Routes[i].Action == "add" {
				plan.Routes[i].Action = planner.TunActionAddExclusive
			}
		}
		for i := range plan.PolicyRules {
			if plan.PolicyRules[i].Action == "add" {
				plan.PolicyRules[i].Action = planner.TunActionAddExclusive
			}
		}
	}
	plan.Steps = xrayOwnedTunSteps(plan)
	plan.RollbackSteps = xrayOwnedTunRollbackSteps(plan)
	return plan
}

func xrayOwnedTunSteps(plan planner.TunPlan) []string {
	steps := []string{"Start Xray native tun inbound and verify podlaz0 before applying podlaz-owned Linux routes, DNS, and nftables state"}
	for _, step := range plan.Steps {
		if strings.Contains(step, "Plan TUN interface") || strings.Contains(step, "Leave TUN devices") {
			continue
		}
		steps = append(steps, step)
	}
	return steps
}

func xrayOwnedTunRollbackSteps(plan planner.TunPlan) []string {
	steps := []string{"Roll back podlaz-owned nftables, DNS, routes, and policy rules before stopping Xray and releasing podlaz0"}
	for _, step := range plan.RollbackSteps {
		if strings.Contains(step, "Delete TUN interface") {
			continue
		}
		steps = append(steps, step)
	}
	return steps
}
