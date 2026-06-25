package executor

import (
	"context"
	"net/netip"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func (e IPRouteExecutor) existingRouteLine(ctx context.Context, plan planner.TunRoutePlan) (string, error) {
	args := []string{"-4", "route", "show", "table", routeTable(plan.Table), plan.Destination}
	result, err := observeCommand(ctx, e.Runner, "ip", args...)
	if err != nil {
		return "", err
	}
	return firstNonEmptyLine(result.Stdout), nil
}

func mainServerBypassRoute(plan planner.TunRoutePlan) bool {
	if routeTable(plan.Table) != planner.MainRoutingTable {
		return false
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(plan.Destination))
	if err != nil {
		return false
	}
	return prefix.Addr().Is4() && prefix.Bits() == 32 && strings.TrimSpace(plan.Gateway) != "" && strings.TrimSpace(plan.Interface) != ""
}
