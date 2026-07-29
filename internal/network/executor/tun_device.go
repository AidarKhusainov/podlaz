package executor

import (
	"context"
	"errors"
	"fmt"
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
	step := Step{Kind: "tun-device", Target: plan.Name, Description: plan.Reason, Owner: OwnerTunDevice}
	if plan.MTU > 0 {
		if err := e.run(ctx, "ip", "link", "set", "dev", plan.Name, "mtu", strconv.Itoa(plan.MTU)); err != nil {
			return step, fmt.Errorf("set TUN device %s MTU: %w", plan.Name, err)
		}
	}
	if err := e.run(ctx, "ip", "link", "set", "dev", plan.Name, "up"); err != nil {
		return step, fmt.Errorf("bring TUN device %s up: %w", plan.Name, err)
	}
	return step, nil
}

func (e IPTunDeviceExecutor) Verify(ctx context.Context, plan planner.TunDevicePlan) error {
	if strings.TrimSpace(plan.Name) == "" {
		return errors.New("missing TUN device name")
	}
	result, err := observeCommand(ctx, e.Runner, "ip", "-details", "link", "show", "dev", plan.Name)
	if err != nil {
		return fmt.Errorf("verify TUN device %s: %w", plan.Name, err)
	}
	if err := verifyTUNLinkDetails(plan, result.Stdout); err != nil {
		return fmt.Errorf("verify TUN device %s: %w", plan.Name, err)
	}
	return nil
}

func verifyTUNLinkDetails(plan planner.TunDevicePlan, output string) error {
	text := strings.TrimSpace(output)
	if text == "" {
		return errors.New("empty ip link details output")
	}
	if !tunLinkOutputIsTun(text) {
		return fmt.Errorf("link is not a TUN device: %s", firstLine(text))
	}
	if plan.MTU > 0 && !linkOutputHasMTU(text, plan.MTU) {
		return fmt.Errorf("link MTU does not match planned MTU %d: %s", plan.MTU, firstLine(text))
	}
	if !linkOutputIsUp(text) {
		return fmt.Errorf("link is not up: %s", firstLine(text))
	}
	return nil
}

func tunLinkOutputIsTun(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		for i := 0; i+2 < len(fields); i++ {
			if fields[i] == "tun" && fields[i+1] == "type" && fields[i+2] == "tun" {
				return true
			}
		}
	}
	return false
}

func linkOutputHasMTU(output string, mtu int) bool {
	want := strconv.Itoa(mtu)
	for _, fieldList := range strings.Split(output, "\n") {
		fields := strings.Fields(fieldList)
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "mtu" && fields[i+1] == want {
				return true
			}
		}
	}
	return false
}

func linkOutputIsUp(output string) bool {
	line := firstLine(output)
	if strings.Contains(line, "state UP") {
		return true
	}
	start := strings.Index(line, "<")
	end := strings.Index(line, ">")
	if start < 0 || end <= start {
		return false
	}
	flags := strings.Split(line[start+1:end], ",")
	for _, flag := range flags {
		if strings.TrimSpace(flag) == "UP" {
			return true
		}
	}
	return false
}

func firstLine(output string) string {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return "<empty>"
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
