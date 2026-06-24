// Package executor applies already-planned privileged Linux networking changes.
package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	OwnerTunDevice  = "podlaz:tun-device"
	OwnerRoute      = "podlaz:route"
	OwnerPolicyRule = "podlaz:policy-rule"
)

const defaultCommandTimeout = 5 * time.Second

const (
	defaultTunDeviceUser  = "podlaz-xray"
	defaultTunDeviceGroup = "podlaz-xray"
)

// CommandResult is a low-level command execution result.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// CommandRunner runs privileged host commands for the OS-backed executor.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

// OSRunner executes commands through os/exec.
type OSRunner struct{}

// Run executes a host command and captures stdout, stderr, and exit code.
func (OSRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := CommandResult{
		Stdout: strings.TrimSpace(stdout.String()),
		Stderr: strings.TrimSpace(stderr.String()),
	}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = -1
	}
	return result, err
}

// TunDeviceExecutor owns TUN interface creation, verification, and cleanup.
type TunDeviceExecutor interface {
	Create(ctx context.Context, plan planner.TunDevicePlan) (Step, error)
	Verify(ctx context.Context, plan planner.TunDevicePlan) error
	Rollback(ctx context.Context, plan planner.TunDevicePlan) error
}

// RouteExecutor owns route apply, verification, and cleanup.
type RouteExecutor interface {
	Add(ctx context.Context, plan planner.TunRoutePlan) (Step, error)
	Verify(ctx context.Context, plan planner.TunRoutePlan) error
	Rollback(ctx context.Context, plan planner.TunRoutePlan) error
}

// PolicyRuleExecutor owns policy-rule apply, verification, and cleanup.
type PolicyRuleExecutor interface {
	Add(ctx context.Context, plan planner.TunPolicyRulePlan) (Step, error)
	Verify(ctx context.Context, plan planner.TunPolicyRulePlan) error
	Rollback(ctx context.Context, plan planner.TunPolicyRulePlan) error
}

// Step records one applied podlaz-owned networking mutation.
type Step struct {
	Kind        string
	Target      string
	Description string
	Owner       string
}

// TunExecutor applies the TUN, route, and policy-rule parts of a TUN plan.
type TunExecutor struct {
	TunDevice   TunDeviceExecutor
	Routes      RouteExecutor
	PolicyRules PolicyRuleExecutor
}

// NewOSExecutor returns the Linux iproute2-backed executor.
func NewOSExecutor() TunExecutor {
	runner := OSRunner{}
	return TunExecutor{
		TunDevice:   IPTunDeviceExecutor{Runner: runner, DeviceUser: defaultTunDeviceUser, DeviceGroup: defaultTunDeviceGroup},
		Routes:      IPRouteExecutor{Runner: runner},
		PolicyRules: IPPolicyRuleExecutor{Runner: runner},
	}
}

// Apply applies TUN, routes, and policy rules from the already-inspected plan.
func (e TunExecutor) Apply(ctx context.Context, plan planner.TunPlan) ([]Step, error) {
	if err := e.validate(); err != nil {
		return nil, err
	}
	steps := make([]Step, 0, 1+len(plan.Routes)+len(plan.PolicyRules))

	step, err := e.TunDevice.Create(ctx, plan.TunDevice)
	if err != nil {
		return steps, err
	}
	steps = append(steps, step)

	for _, route := range plan.Routes {
		if route.Action != "add" {
			continue
		}
		step, err := e.Routes.Add(ctx, route)
		if err != nil {
			return steps, err
		}
		steps = append(steps, step)
	}
	for _, rule := range plan.PolicyRules {
		if rule.Action != "add" {
			continue
		}
		step, err := e.PolicyRules.Add(ctx, rule)
		if err != nil {
			return steps, err
		}
		steps = append(steps, step)
	}
	return steps, nil
}

// Verify checks that every applied TUN, route, and policy-rule target is visible.
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

// Rollback removes policy rules, routes, and TUN interface in reverse dependency order.
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
	if err := e.TunDevice.Rollback(ctx, plan.TunDevice); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
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

// IPTunDeviceExecutor applies TUN device operations through iproute2.
type IPTunDeviceExecutor struct {
	Runner      CommandRunner
	DeviceUser  string
	DeviceGroup string
}

func (e IPTunDeviceExecutor) Create(ctx context.Context, plan planner.TunDevicePlan) (Step, error) {
	if plan.Name == "" {
		return Step{}, errors.New("missing TUN device name")
	}
	args := []string{"tuntap", "add", "dev", plan.Name, "mode", "tun"}
	if user := strings.TrimSpace(e.DeviceUser); user != "" {
		args = append(args, "user", user)
	}
	if group := strings.TrimSpace(e.DeviceGroup); group != "" {
		args = append(args, "group", group)
	}
	if err := e.run(ctx, "ip", args...); err != nil {
		return Step{}, fmt.Errorf("create TUN device %s: %w", plan.Name, err)
	}
	if plan.MTU > 0 {
		if err := e.run(ctx, "ip", "link", "set", "dev", plan.Name, "mtu", strconv.Itoa(plan.MTU)); err != nil {
			return Step{}, fmt.Errorf("set TUN device %s MTU: %w", plan.Name, err)
		}
	}
	if err := e.run(ctx, "ip", "link", "set", "dev", plan.Name, "up"); err != nil {
		return Step{}, fmt.Errorf("bring TUN device %s up: %w", plan.Name, err)
	}
	return Step{Kind: "tun-device", Target: plan.Name, Description: plan.Reason, Owner: OwnerTunDevice}, nil
}

func (e IPTunDeviceExecutor) Verify(ctx context.Context, plan planner.TunDevicePlan) error {
	if err := e.run(ctx, "ip", "link", "show", "dev", plan.Name); err != nil {
		return fmt.Errorf("verify TUN device %s: %w", plan.Name, err)
	}
	return nil
}

func (e IPTunDeviceExecutor) Rollback(ctx context.Context, plan planner.TunDevicePlan) error {
	if plan.Name == "" {
		return nil
	}
	if err := e.run(ctx, "ip", "link", "del", "dev", plan.Name); err != nil && !resourceMissing(err) {
		return fmt.Errorf("delete TUN device %s: %w", plan.Name, err)
	}
	return nil
}

func (e IPTunDeviceExecutor) run(ctx context.Context, name string, args ...string) error {
	return runCommand(ctx, e.Runner, name, args...)
}

// IPRouteExecutor applies route operations through iproute2.
type IPRouteExecutor struct {
	Runner CommandRunner
}

func (e IPRouteExecutor) Add(ctx context.Context, plan planner.TunRoutePlan) (Step, error) {
	args := routeArgs("add", plan)
	if err := runCommand(ctx, e.Runner, "ip", args...); err != nil {
		return Step{}, fmt.Errorf("add route %s table %s: %w", plan.Destination, plan.Table, err)
	}
	if err := flushIPv4RouteCache(ctx, e.Runner); err != nil {
		return Step{}, fmt.Errorf("flush IPv4 route cache after add route %s table %s: %w", plan.Destination, plan.Table, err)
	}
	return Step{Kind: "route", Target: routeTarget(plan), Description: plan.Reason, Owner: OwnerRoute}, nil
}

func (e IPRouteExecutor) Verify(ctx context.Context, plan planner.TunRoutePlan) error {
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

// IPPolicyRuleExecutor applies policy-rule operations through iproute2.
type IPPolicyRuleExecutor struct {
	Runner CommandRunner
}

func (e IPPolicyRuleExecutor) Add(ctx context.Context, plan planner.TunPolicyRulePlan) (Step, error) {
	args := ruleArgs("add", plan)
	if err := runCommand(ctx, e.Runner, "ip", args...); err != nil {
		return Step{}, fmt.Errorf("add policy rule priority %d: %w", plan.Priority, err)
	}
	if err := flushIPv4RouteCache(ctx, e.Runner); err != nil {
		return Step{}, fmt.Errorf("flush IPv4 route cache after add policy rule priority %d: %w", plan.Priority, err)
	}
	return Step{Kind: "policy-rule", Target: ruleTarget(plan), Description: plan.Reason, Owner: OwnerPolicyRule}, nil
}

func (e IPPolicyRuleExecutor) Verify(ctx context.Context, plan planner.TunPolicyRulePlan) error {
	args := []string{"-4", "rule", "show", "priority", strconv.Itoa(plan.Priority)}
	result, err := observeCommand(ctx, e.Runner, "ip", args...)
	if err != nil {
		return fmt.Errorf("verify policy rule priority %d: %w", plan.Priority, err)
	}
	line := firstNonEmptyLine(result.Stdout)
	if line == "" {
		return fmt.Errorf("verify policy rule priority %d: rule not found", plan.Priority)
	}
	if err := verifyPolicyRuleLine(line, plan); err != nil {
		return fmt.Errorf("verify policy rule priority %d: %w", plan.Priority, err)
	}
	return nil
}

func (e IPPolicyRuleExecutor) Rollback(ctx context.Context, plan planner.TunPolicyRulePlan) error {
	args := ruleArgs("del", plan)
	if err := runCommand(ctx, e.Runner, "ip", args...); err != nil && !resourceMissing(err) {
		return fmt.Errorf("delete policy rule priority %d: %w", plan.Priority, err)
	}
	if err := flushIPv4RouteCache(ctx, e.Runner); err != nil {
		return fmt.Errorf("flush IPv4 route cache after delete policy rule priority %d: %w", plan.Priority, err)
	}
	return nil
}

func runCommand(ctx context.Context, runner CommandRunner, name string, args ...string) error {
	_, err := observeCommand(ctx, runner, name, args...)
	return err
}

func observeCommand(ctx context.Context, runner CommandRunner, name string, args ...string) (CommandResult, error) {
	if runner == nil {
		runner = OSRunner{}
	}
	cmdCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	result, err := runner.Run(cmdCtx, name, args...)
	if err == nil && result.ExitCode == 0 {
		return result, nil
	}
	return result, commandError{name: name, args: args, result: result, err: err}
}

type commandError struct {
	name   string
	args   []string
	result CommandResult
	err    error
}

func (e commandError) Error() string {
	parts := []string{e.name + " " + strings.Join(e.args, " ")}
	if e.result.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit code %d", e.result.ExitCode))
	}
	if strings.TrimSpace(e.result.Stderr) != "" {
		parts = append(parts, "stderr: "+strings.TrimSpace(e.result.Stderr))
	}
	if e.err != nil && strings.TrimSpace(e.result.Stderr) == "" {
		parts = append(parts, e.err.Error())
	}
	return strings.Join(parts, ": ")
}

func flushIPv4RouteCache(ctx context.Context, runner CommandRunner) error {
	return runCommand(ctx, runner, "ip", "-4", "route", "flush", "cache")
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

func ruleArgs(op string, plan planner.TunPolicyRulePlan) []string {
	args := []string{"-4", "rule", op, "priority", strconv.Itoa(plan.Priority)}
	selectorFields := strings.Fields(plan.Selector)
	args = append(args, selectorFields...)
	args = append(args, "lookup", routeTable(plan.Table))
	return args
}

func verifyRouteLine(line string, plan planner.TunRoutePlan) error {
	fields := strings.Fields(line)
	if plan.Destination != planner.IPv4DefaultRoute && !containsField(fields, plan.Destination) {
		return fmt.Errorf("destination mismatch in %q", line)
	}
	if plan.Interface != "" && !containsAdjacentFields(fields, "dev", plan.Interface) {
		return fmt.Errorf("interface mismatch: expected dev %s in %q", plan.Interface, line)
	}
	if plan.Gateway != "" && !containsAdjacentFields(fields, "via", plan.Gateway) {
		return fmt.Errorf("gateway mismatch: expected via %s in %q", plan.Gateway, line)
	}
	return nil
}

func verifyPolicyRuleLine(line string, plan planner.TunPolicyRulePlan) error {
	fields := normalizeRuleFields(strings.Fields(line))
	for _, field := range strings.Fields(plan.Selector) {
		if !containsField(fields, field) {
			return fmt.Errorf("selector mismatch: expected %q in %q", plan.Selector, line)
		}
	}
	expectedTable := routeTable(plan.Table)
	if !containsLookupTable(fields, expectedTable) {
		return fmt.Errorf("lookup table mismatch: expected %s in %q", expectedTable, line)
	}
	return nil
}

func normalizeRuleFields(fields []string) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		out = append(out, strings.TrimSuffix(field, ":"))
	}
	return out
}

func containsLookupTable(fields []string, table string) bool {
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == "lookup" && (fields[i+1] == table || routeTable(fields[i+1]) == table) {
			return true
		}
	}
	return false
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

func ruleTarget(plan planner.TunPolicyRulePlan) string {
	return fmt.Sprintf("priority %d %s lookup %s", plan.Priority, plan.Selector, plan.Table)
}

func resourceMissing(err error) bool {
	return commandErrorContains(err, "does not exist", "cannot find device", "no such process", "no such file or directory", "no such table", "no such file")
}

func commandErrorContains(err error, needles ...string) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
