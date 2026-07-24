package executor

import (
	"context"
	"fmt"
	"os/user"
	"strconv"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

const (
	defaultTunDeviceUser  = "podlaz-xray"
	defaultTunDeviceGroup = "podlaz-xray"
)

type IPTunDeviceExecutor struct {
	Runner      CommandRunner
	DeviceUser  string
	DeviceGroup string
}

func (e IPTunDeviceExecutor) Create(ctx context.Context, plan planner.TunDevicePlan) (Step, error) {
	if err := e.validateOwnership(); err != nil {
		return Step{}, err
	}
	args := []string{"tuntap", "add", "dev", plan.Name, "mode", "tun", "user", e.DeviceUser, "group", e.DeviceGroup}
	if err := runCommand(ctx, e.Runner, "ip", args...); err != nil {
		return Step{}, fmt.Errorf("create TUN device %s: %w", plan.Name, err)
	}
	step := Step{Kind: "tun-device", Target: plan.Name, Description: plan.Reason, Owner: OwnerTunDevice}
	if plan.MTU > 0 {
		if err := runCommand(ctx, e.Runner, "ip", "link", "set", "dev", plan.Name, "mtu", strconv.Itoa(plan.MTU)); err != nil {
			return step, fmt.Errorf("set TUN device %s MTU: %w", plan.Name, err)
		}
	}
	if err := runCommand(ctx, e.Runner, "ip", "link", "set", "dev", plan.Name, "up"); err != nil {
		return step, fmt.Errorf("bring TUN device %s up: %w", plan.Name, err)
	}
	return step, nil
}

func (e IPTunDeviceExecutor) Verify(ctx context.Context, plan planner.TunDevicePlan) error {
	result, err := observeCommand(ctx, e.Runner, "ip", "-details", "link", "show", "dev", plan.Name)
	if err != nil {
		return fmt.Errorf("verify TUN device %s: %w", plan.Name, err)
	}
	if err := verifyTunDeviceOutput(result.Stdout, plan); err != nil {
		return fmt.Errorf("verify TUN device %s: %w", plan.Name, err)
	}
	return nil
}

func (e IPTunDeviceExecutor) Rollback(ctx context.Context, plan planner.TunDevicePlan) error {
	if err := runCommand(ctx, e.Runner, "ip", "link", "del", "dev", plan.Name); err != nil && !resourceMissing(err) {
		return fmt.Errorf("delete TUN device %s: %w", plan.Name, err)
	}
	return nil
}

func (e IPTunDeviceExecutor) validateOwnership() error {
	userName := strings.TrimSpace(e.DeviceUser)
	groupName := strings.TrimSpace(e.DeviceGroup)
	if userName == "" || groupName == "" {
		return fmt.Errorf("missing TUN device user/group ownership")
	}
	if _, err := user.Lookup(userName); err != nil {
		return fmt.Errorf("lookup TUN device user %s: %w", userName, err)
	}
	if _, err := user.LookupGroup(groupName); err != nil {
		return fmt.Errorf("lookup TUN device group %s: %w", groupName, err)
	}
	return nil
}

func verifyTunDeviceOutput(output string, plan planner.TunDevicePlan) error {
	if strings.TrimSpace(output) == "" {
		return fmt.Errorf("device not found")
	}
	if !strings.Contains(output, plan.Name+":") {
		return fmt.Errorf("interface name mismatch")
	}
	if !strings.Contains(strings.ToLower(output), "tun") {
		return fmt.Errorf("interface is not reported as TUN")
	}
	if plan.MTU > 0 && !strings.Contains(output, "mtu "+strconv.Itoa(plan.MTU)) {
		return fmt.Errorf("MTU mismatch")
	}
	if !strings.Contains(output, "<") || !strings.Contains(output, "UP") {
		return fmt.Errorf("interface is not up")
	}
	return nil
}
