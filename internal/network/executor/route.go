package executor

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

type IPRouteExecutor struct {
	Runner                      CommandRunner
	AllocationEvidenceCollector func(context.Context) (netsnapshot.TunAllocationEvidence, error)
}

func (e IPRouteExecutor) Add(ctx context.Context, plan planner.TunRoutePlan) (Step, error) {
	if exclusiveAllocatedRouteTable(plan) {
		if err := e.verifyAllocatedRoutingTableAuthority(ctx, plan, 0); err != nil {
			return Step{}, fmt.Errorf("inspect allocated routing table %s before apply: %w", plan.Table, err)
		}
	} else if mainServerBypassRoute(plan) {
		line, err := e.existingRouteLine(ctx, plan)
		if err != nil {
			return Step{}, fmt.Errorf("inspect existing route %s table %s: %w", plan.Destination, plan.Table, err)
		}
		if line != "" {
			if plan.Action == planner.TunActionAddExclusive {
				return Step{}, fmt.Errorf("allocated route %s table %s became occupied before apply", plan.Destination, plan.Table)
			}
			if err := verifyRouteLine(line, plan); err != nil {
				return Step{}, fmt.Errorf("existing route %s table %s differs from planned server bypass: %w", plan.Destination, plan.Table, err)
			}
			return Step{}, nil
		}
	}

	args := routeArgs("add", plan)
	if err := runCommand(ctx, e.Runner, "ip", args...); err != nil {
		return Step{}, fmt.Errorf("add route %s table %s: %w", plan.Destination, plan.Table, err)
	}
	step := Step{Kind: "route", Target: routeTarget(plan), Description: plan.Reason, Owner: OwnerRoute}
	if exclusiveAllocatedRouteTable(plan) {
		if err := e.verifyAllocatedRoutingTableAuthority(ctx, plan, 1); err != nil {
			return step, fmt.Errorf("revalidate allocated routing table %s after add: %w", plan.Table, err)
		}
		if err := e.verifyExclusiveAllocatedRouteTable(ctx, plan); err != nil {
			return step, fmt.Errorf("verify allocated routing table %s after add: %w", plan.Table, err)
		}
	}
	if err := flushIPv4RouteCache(ctx, e.Runner); err != nil {
		return step, fmt.Errorf("flush IPv4 route cache after add route %s table %s: %w", plan.Destination, plan.Table, err)
	}
	return step, nil
}

func (e IPRouteExecutor) Verify(ctx context.Context, plan planner.TunRoutePlan) error {
	if exclusiveAllocatedRouteTable(plan) {
		if err := e.verifyExclusiveAllocatedRouteTable(ctx, plan); err != nil {
			return fmt.Errorf("verify route %s table %s: %w", plan.Destination, plan.Table, err)
		}
		return nil
	}

	args := []string{"-4", "route", "show", "table", routeTable(plan.Table), plan.Destination}
	result, err := observeCommand(ctx, e.Runner, "ip", args...)
	if err != nil {
		return fmt.Errorf("verify route %s table %s: %w", plan.Destination, plan.Table, err)
	}
	line := firstNonEmptyLine(result.Stdout)
	if line == "" {
		return fmt.Errorf("verify route %s table %s: route not found", plan.Destination, plan.Table)
	}
	if err := verifyRouteLine(line, plan); err != nil {
		return fmt.Errorf("verify route %s table %s: %w", plan.Destination, plan.Table, err)
	}
	return nil
}

func (e IPRouteExecutor) Rollback(ctx context.Context, plan planner.TunRoutePlan) error {
	args := routeArgs("del", plan)
	if err := runCommand(ctx, e.Runner, "ip", args...); err != nil && !resourceMissing(err) {
		return fmt.Errorf("delete route %s table %s: %w", plan.Destination, plan.Table, err)
	}
	if err := flushIPv4RouteCache(ctx, e.Runner); err != nil {
		return fmt.Errorf("flush IPv4 route cache after delete route %s table %s: %w", plan.Destination, plan.Table, err)
	}
	return nil
}

func exclusiveAllocatedRouteTable(plan planner.TunRoutePlan) bool {
	return strings.TrimSpace(plan.Action) == planner.TunActionAddExclusive && planner.IsAllocatedTunRoutingTable(routeTable(plan.Table))
}

func (e IPRouteExecutor) allocatedRouteTableLines(ctx context.Context, plan planner.TunRoutePlan) ([]string, error) {
	result, err := observeCommand(ctx, e.Runner, "ip", "-N", "-4", "-o", "route", "show", "table", routeTable(plan.Table))
	if err != nil {
		if resourceMissing(err) {
			return nil, nil
		}
		return nil, err
	}
	return nonEmptyLines(result.Stdout), nil
}

func (e IPRouteExecutor) collectAllocationEvidence(ctx context.Context) (netsnapshot.TunAllocationEvidence, error) {
	if e.AllocationEvidenceCollector != nil {
		return e.AllocationEvidenceCollector(ctx)
	}
	return netsnapshot.CollectTunAllocationEvidence(ctx)
}

func (e IPRouteExecutor) verifyAllocatedRoutingTableAuthority(ctx context.Context, plan planner.TunRoutePlan, wantRoutes int) error {
	tableText := routeTable(plan.Table)
	tableValue, err := strconv.ParseUint(tableText, 10, 32)
	if err != nil || tableValue == 0 {
		return fmt.Errorf("invalid allocated routing table identity %q", tableText)
	}
	table := uint32(tableValue)

	evidence, err := e.collectAllocationEvidence(ctx)
	if err != nil {
		return fmt.Errorf("collect authoritative TUN allocation evidence: %w", err)
	}

	routes := 0
	for _, route := range evidence.IPv4Routes {
		if route.Table == table {
			routes++
		}
	}
	if routes != wantRoutes {
		return fmt.Errorf("allocated routing table %d has %d route(s), want %d", table, routes, wantRoutes)
	}

	for _, rule := range evidence.IPv4PolicyRules {
		if rule.Table == table {
			return fmt.Errorf("allocated routing table %d is referenced by foreign policy rule priority %d", table, rule.Priority)
		}
	}
	for _, reserved := range evidence.ReservedRoutingTables {
		if reserved == table {
			return fmt.Errorf("allocated routing table %d is reserved by a foreign VRF", table)
		}
	}
	return nil
}

func (e IPRouteExecutor) verifyExclusiveAllocatedRouteTable(ctx context.Context, plan planner.TunRoutePlan) error {
	lines, err := e.allocatedRouteTableLines(ctx, plan)
	if err != nil {
		return err
	}
	if len(lines) != 1 {
		return fmt.Errorf("allocated routing table must contain exactly one session route, found %d", len(lines))
	}
	if err := verifyRouteLine(lines[0], plan); err != nil {
		return fmt.Errorf("exclusive session route mismatch: %w", err)
	}
	return nil
}

func routeArgs(op string, plan planner.TunRoutePlan) []string {
	args := []string{"-4", "route", op, plan.Destination}
	if plan.Gateway != "" {
		args = append(args, "via", plan.Gateway)
	}
	if plan.Interface != "" {
		args = append(args, "dev", plan.Interface)
	}
	args = append(args, "table", routeTable(plan.Table))
	return args
}

func verifyRouteLine(line string, plan planner.TunRoutePlan) error {
	fields := strings.Fields(line)
	if !routeDestinationMatches(fields, plan.Destination) {
		return fmt.Errorf("destination mismatch: expected %s in %q", plan.Destination, line)
	}
	if plan.Interface != "" && !containsAdjacentFields(fields, "dev", plan.Interface) {
		return fmt.Errorf("interface mismatch: expected dev %s in %q", plan.Interface, line)
	}
	if plan.Gateway != "" && !containsAdjacentFields(fields, "via", plan.Gateway) {
		return fmt.Errorf("gateway mismatch: expected via %s in %q", plan.Gateway, line)
	}
	return nil
}

func routeDestinationMatches(fields []string, destination string) bool {
	if len(fields) == 0 {
		return false
	}
	if destination == planner.IPv4DefaultRoute {
		return fields[0] == planner.IPv4DefaultRoute || fields[0] == "0.0.0.0/0"
	}
	return containsField(fields, destination)
}

func containsAdjacentFields(fields []string, first, second string) bool {
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == first && routeTokenMatches(fields[i+1], second) {
			return true
		}
	}
	return false
}

func containsField(fields []string, want string) bool {
	for _, field := range fields {
		if routeTokenMatches(field, want) {
			return true
		}
	}
	return false
}

func routeTokenMatches(got, want string) bool {
	if got == want {
		return true
	}
	if strings.HasSuffix(want, "/32") && got == strings.TrimSuffix(want, "/32") {
		return true
	}
	if strings.HasSuffix(got, "/32") && strings.TrimSuffix(got, "/32") == want {
		return true
	}
	return false
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func routeTable(table string) string {
	if table == planner.TunRoutingTable {
		return strconv.Itoa(planner.TunRoutingTableID)
	}
	if table == "" {
		return planner.MainRoutingTable
	}
	return table
}

func routeTarget(plan planner.TunRoutePlan) string {
	return plan.Table + " " + plan.Destination
}
